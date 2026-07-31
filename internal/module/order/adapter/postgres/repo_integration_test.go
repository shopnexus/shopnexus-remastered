//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"shopnexus/internal/infra/postgres"
	orderpg "shopnexus/internal/module/order/adapter/postgres"
	"shopnexus/internal/module/order/domain"
	"shopnexus/internal/module/order/port"
)

func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("ORDER_DB_DSN")
	if dsn == "" {
		t.Skip("ORDER_DB_DSN not set")
	}
	return dsn
}

func newRepo(t *testing.T) (*orderpg.Repo, *pgxpool.Pool) {
	t.Helper()
	pool, err := postgres.NewPool(context.Background(), testDSN(t), "order")
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return orderpg.New(pool), pool
}

// party keeps one run's rows out of another's: the tables are append-only across runs, so
// a fixed account id would make a list assertion depend on history.
func party(t *testing.T) (buyer, seller int64) {
	t.Helper()
	base := time.Now().UnixNano() % 1_000_000_000
	return base, base + 1
}

func address() domain.AddressSnapshot {
	return domain.AddressSnapshot{
		FullName: "Nguyen Van A", Phone: "+84900000001", Country: "VN",
		ProvinceCode: "79", WardCode: "26734", AddressDetail: new("12 Le Loi"),
	}
}

// draft writes a checkout session to hang items and orders off, since both carry a real FK
// to it.
func draft(t *testing.T, r *orderpg.Repo, buyer, seller int64) domain.Draft {
	t.Helper()
	d, err := domain.NewDraft(buyer, domain.ListingSnapshot{
		ListingID: 500, SellerID: seller, Name: "Ao thun", Currency: "VND", PriceMode: "fixed",
		Variants: []domain.VariantSnapshot{{VariantID: 501, Price: 100_000}},
	}, 30*time.Minute)
	if err != nil {
		t.Fatalf("NewDraft: %v", err)
	}
	if err := r.InsertDraft(context.Background(), &d); err != nil {
		t.Fatalf("InsertDraft: %v", err)
	}
	return d
}

// paidItem is one checked-out line: it exists before the money lands, which is what a nil
// order_id means.
func paidItem(t *testing.T, r *orderpg.Repo, origin domain.Origin, buyer, seller, sessionID int64) domain.Item {
	t.Helper()
	i, err := domain.NewItem(origin, buyer, seller, 500, 501, address(), "", "VND", 1,
		"ghn-express", 100_000, sessionID)
	if err != nil {
		t.Fatalf("NewItem: %v", err)
	}
	if err := r.InsertItems(context.Background(), []*domain.Item{&i}); err != nil {
		t.Fatalf("InsertItems: %v", err)
	}
	return i
}

// placedOrder takes a draft all the way to an order, which is the state every later test
// starts from.
func placedOrder(t *testing.T, r *orderpg.Repo) (domain.Order, domain.Item) {
	t.Helper()
	ctx := context.Background()
	buyer, seller := party(t)
	d := draft(t, r, buyer, seller)
	item := paidItem(t, r, domain.FromDraft(d.ID), buyer, seller, time.Now().UnixNano()%1_000_000_000)
	transportID, err := r.InsertTransport(ctx, "ghn-express")
	if err != nil {
		t.Fatalf("InsertTransport: %v", err)
	}
	o, err := domain.NewOrder(domain.FromDraft(d.ID), buyer, seller, transportID, address(), address())
	if err != nil {
		t.Fatalf("NewOrder: %v", err)
	}
	if err := r.CreateOrder(ctx, &o, []int64{item.ID}); err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	return o, item
}

// The webhook's contract: the money writes the order once, links the lines it paid for, and
// a redelivery loses to the unique origin rather than minting a second order.
func TestCreateOrder_OriginIsIdempotent(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	o, item := placedOrder(t, r)

	items, err := r.OrderItems(ctx, o.ID)
	if err != nil {
		t.Fatalf("OrderItems: %v", err)
	}
	if len(items) != 1 || items[0].ID != item.ID || items[0].OrderID == nil {
		t.Fatalf("items = %+v, want the paid line linked", items)
	}
	// The address survived the JSON column, district code and all.
	if items[0].Address.ProvinceCode != "79" || items[0].Address.DistrictCode != nil {
		t.Errorf("address = %+v, want it round-tripped with a null district", items[0].Address)
	}

	transportID, err := r.InsertTransport(ctx, "ghn-express")
	if err != nil {
		t.Fatalf("InsertTransport: %v", err)
	}
	second, err := domain.NewOrder(domain.Origin{DraftID: o.DraftID}, o.BuyerID, o.SellerID,
		transportID, address(), address())
	if err != nil {
		t.Fatalf("NewOrder: %v", err)
	}
	if err := r.CreateOrder(ctx, &second, []int64{item.ID}); !errors.Is(err, domain.ErrOrderSettled) {
		t.Fatalf("second CreateOrder = %v, want ErrOrderSettled", err)
	}

	found, err := r.FindOrderByOrigin(ctx, domain.Origin{DraftID: o.DraftID})
	if err != nil {
		t.Fatalf("FindOrderByOrigin: %v", err)
	}
	if found.ID != o.ID {
		t.Fatalf("found = %d, want the first order %d", found.ID, o.ID)
	}
}

// A cancelled line is not linked: the buyer was refunded for it at checkout time, so an
// order that covered it would be charging for something voided.
func TestCreateOrder_SkipsCancelledLines(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	buyer, seller := party(t)
	d := draft(t, r, buyer, seller)
	session := time.Now().UnixNano() % 1_000_000_000
	live := paidItem(t, r, domain.FromDraft(d.ID), buyer, seller, session)
	voided := paidItem(t, r, domain.FromDraft(d.ID), buyer, seller, session)

	if err := voided.Cancel(buyer); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if err := r.SaveItem(ctx, voided); err != nil {
		t.Fatalf("SaveItem: %v", err)
	}

	// Both lines came from the same session, which is what the webhook looks them up by.
	paid, err := r.ItemsByPaymentSession(ctx, session)
	if err != nil {
		t.Fatalf("ItemsByPaymentSession: %v", err)
	}
	if len(paid) != 2 {
		t.Fatalf("paid = %d lines, want both", len(paid))
	}

	transportID, err := r.InsertTransport(ctx, "ghn-express")
	if err != nil {
		t.Fatalf("InsertTransport: %v", err)
	}
	o, err := domain.NewOrder(domain.FromDraft(d.ID), buyer, seller, transportID, address(), address())
	if err != nil {
		t.Fatalf("NewOrder: %v", err)
	}
	if err := r.CreateOrder(ctx, &o, []int64{live.ID, voided.ID}); err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	items, err := r.OrderItems(ctx, o.ID)
	if err != nil {
		t.Fatalf("OrderItems: %v", err)
	}
	if len(items) != 1 || items[0].ID != live.ID {
		t.Fatalf("items = %+v, want only the live line", items)
	}
}

// The escrow window: an order is due once its receipt is old enough, and a live refund
// holds it back — which is the guard that stops paying a seller money under dispute.
func TestPayoutDue_HeldBackByALiveRefund(t *testing.T) {
	r, pool := newRepo(t)
	ctx := context.Background()
	o, _ := placedOrder(t, r)

	// The unboxing evidence is an array column, so the ids stand on their own here — the
	// service is what checks they name confirmed uploads.
	if err := o.ConfirmReceipt([]int64{7001}); err != nil {
		t.Fatalf("ConfirmReceipt: %v", err)
	}
	if err := r.SaveOrder(ctx, o); err != nil {
		t.Fatalf("SaveOrder: %v", err)
	}
	if due, err := r.PayoutDue(ctx, time.Now(), 100); err != nil || contains(due, o.ID) {
		t.Fatalf("PayoutDue = %v, %v; want the window still open", ids(due), err)
	}

	// Wind the receipt back past the window, which is what the clock would do.
	past := time.Now().Add(-domain.PayoutWindow - time.Hour)
	if _, err := pool.Exec(ctx, `UPDATE "order" SET received_at = $1 WHERE id = $2`, past, o.ID); err != nil {
		t.Fatalf("backdate receipt: %v", err)
	}
	due, err := r.PayoutDue(ctx, time.Now(), 100)
	if err != nil {
		t.Fatalf("PayoutDue: %v", err)
	}
	if !contains(due, o.ID) {
		t.Fatalf("PayoutDue = %v, want the order now due", ids(due))
	}

	refund, err := domain.NewRefund(o.ID, o.BuyerID, "not as described", nil)
	if err != nil {
		t.Fatalf("NewRefund: %v", err)
	}
	if err := r.InsertRefund(ctx, &refund); err != nil {
		t.Fatalf("InsertRefund: %v", err)
	}
	due, err = r.PayoutDue(ctx, time.Now(), 100)
	if err != nil {
		t.Fatalf("PayoutDue: %v", err)
	}
	if contains(due, o.ID) {
		t.Fatalf("PayoutDue = %v, want the live refund to hold it", ids(due))
	}

	// The seller refuses, which puts the buyer on the clock rather than closing anything —
	// so the payout stays held while the buyer can still escalate.
	if err := refund.Reject("sent as described"); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if err := r.SaveRefund(ctx, refund); err != nil {
		t.Fatalf("SaveRefund: %v", err)
	}
	due, err = r.PayoutDue(ctx, time.Now(), 100)
	if err != nil {
		t.Fatalf("PayoutDue: %v", err)
	}
	if contains(due, o.ID) {
		t.Fatalf("PayoutDue = %v, want it held while the buyer can still escalate", ids(due))
	}

	// The buyer lets that window pass, the case closes, and the money is the seller's.
	if err := refund.LapseBuyerAction(); err != nil {
		t.Fatalf("LapseBuyerAction: %v", err)
	}
	if err := r.SaveRefund(ctx, refund); err != nil {
		t.Fatalf("SaveRefund: %v", err)
	}
	due, err = r.PayoutDue(ctx, time.Now(), 100)
	if err != nil {
		t.Fatalf("PayoutDue: %v", err)
	}
	if !contains(due, o.ID) {
		t.Fatalf("PayoutDue = %v, want it due again once the refund closed", ids(due))
	}

	// Paying it out takes it off the list for good.
	if err := o.Complete(9001); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if err := r.SaveOrder(ctx, o); err != nil {
		t.Fatalf("SaveOrder: %v", err)
	}
	due, err = r.PayoutDue(ctx, time.Now(), 100)
	if err != nil {
		t.Fatalf("PayoutDue: %v", err)
	}
	if contains(due, o.ID) {
		t.Fatalf("PayoutDue = %v, want a completed order gone", ids(due))
	}
}

// One live refund per order, held by the partial index rather than by the service alone:
// two concurrent requests each see a world where their own claim is the only one.
func TestInsertRefund_OneLivePerOrder(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	o, _ := placedOrder(t, r)

	first, err := domain.NewRefund(o.ID, o.BuyerID, "damaged", nil)
	if err != nil {
		t.Fatalf("NewRefund: %v", err)
	}
	if err := r.InsertRefund(ctx, &first); err != nil {
		t.Fatalf("InsertRefund: %v", err)
	}
	second, err := domain.NewRefund(o.ID, o.BuyerID, "damaged again", nil)
	if err != nil {
		t.Fatalf("NewRefund: %v", err)
	}
	if err := r.InsertRefund(ctx, &second); !errors.Is(err, domain.ErrRefundAlreadyOpen) {
		t.Fatalf("second InsertRefund = %v, want ErrRefundAlreadyOpen", err)
	}

	open, err := r.FindOpenRefundByOrder(ctx, o.ID)
	if err != nil {
		t.Fatalf("FindOpenRefundByOrder: %v", err)
	}
	if open.ID != first.ID {
		t.Fatalf("open = %d, want the first %d", open.ID, first.ID)
	}
}

// The timeout pass: one query finds every refund whose clock has run out, whichever of the
// three windows it was on, and skips the two states that wait on a carrier or a moderator.
func TestOverdueRefunds_AllThreeWindowsOnePass(t *testing.T) {
	r, pool := newRepo(t)
	ctx := context.Background()
	past := time.Now().Add(-time.Hour)

	// A seller who has not answered, and a buyer who has not acted.
	lapsing, _ := placedOrder(t, r)
	sellerSilent := overdue(t, r, pool, lapsing, past, func(ref *domain.Refund) error { return nil })
	buyerSilent := overdue(t, r, pool, second(t, r), past, func(ref *domain.Refund) error {
		return ref.Reject("sent as described")
	})
	// A dispute waits on a moderator, so it carries no deadline and no timer can touch it.
	disputed := overdue(t, r, pool, second(t, r), past, func(ref *domain.Refund) error {
		if err := ref.Reject("sent as described"); err != nil {
			return err
		}
		return ref.Escalate()
	})

	overdueList, err := r.OverdueRefunds(ctx, time.Now(), 200)
	if err != nil {
		t.Fatalf("OverdueRefunds: %v", err)
	}
	got := map[int64]string{}
	for _, ref := range overdueList {
		got[ref.ID] = ref.Status
	}
	if got[sellerSilent] != domain.RefundAwaitingSeller {
		t.Errorf("seller window = %q, want it overdue", got[sellerSilent])
	}
	if got[buyerSilent] != domain.RefundAwaitingBuyer {
		t.Errorf("buyer window = %q, want it overdue", got[buyerSilent])
	}
	if _, ok := got[disputed]; ok {
		t.Errorf("a disputed refund is on the timer's list; it waits on a moderator")
	}
}

// second is another placed order, for a test that needs more than one refund.
func second(t *testing.T, r *orderpg.Repo) domain.Order {
	t.Helper()
	o, _ := placedOrder(t, r)
	return o
}

// overdue opens a refund, applies a transition, and backdates its deadline so the timeout
// pass sees it. It returns the refund's id.
func overdue(t *testing.T, r *orderpg.Repo, pool *pgxpool.Pool, o domain.Order, deadline time.Time,
	move func(*domain.Refund) error) int64 {
	t.Helper()
	ctx := context.Background()
	ref, err := domain.NewRefund(o.ID, o.BuyerID, "not as described", nil)
	if err != nil {
		t.Fatalf("NewRefund: %v", err)
	}
	if err := r.InsertRefund(ctx, &ref); err != nil {
		t.Fatalf("InsertRefund: %v", err)
	}
	if err := move(&ref); err != nil {
		t.Fatalf("transition: %v", err)
	}
	if err := r.SaveRefund(ctx, ref); err != nil {
		t.Fatalf("SaveRefund: %v", err)
	}
	// The CHECK ties a deadline to the status, so only a status that has one is backdated.
	if ref.DeadlineAt != nil {
		if _, err := pool.Exec(ctx, `UPDATE refund SET deadline_at = $1 WHERE id = $2`, deadline, ref.ID); err != nil {
			t.Fatalf("backdate deadline: %v", err)
		}
	}
	return ref.ID
}

// One active negotiation per (buyer, variant): revising terms happens in place, so a second
// active offer would be two answers to the same question.
func TestInsertOffer_OneActivePerBuyerAndVariant(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	buyer, seller := party(t)

	first, err := domain.NewOffer(500, 501, buyer, buyer, seller, 1, 80_000, "bundle", 48*time.Hour)
	if err != nil {
		t.Fatalf("NewOffer: %v", err)
	}
	if err := r.InsertOffer(ctx, &first); err != nil {
		t.Fatalf("InsertOffer: %v", err)
	}
	dup, err := domain.NewOffer(500, 501, buyer, buyer, seller, 1, 70_000, "again", 48*time.Hour)
	if err != nil {
		t.Fatalf("NewOffer: %v", err)
	}
	if err := r.InsertOffer(ctx, &dup); !errors.Is(err, domain.ErrOfferAlreadyOpen) {
		t.Fatalf("second InsertOffer = %v, want ErrOfferAlreadyOpen", err)
	}

	active, err := r.FindActiveOffer(ctx, buyer, 501)
	if err != nil {
		t.Fatalf("FindActiveOffer: %v", err)
	}
	if active.ID != first.ID || active.Total != 80_000 {
		t.Fatalf("active = %+v, want the first offer", active)
	}

	// A counter revises the same row, and the terms that come back are the new ones.
	if err := active.Counter(seller, 1, 90_000, "firm", time.Now(), 48*time.Hour); err != nil {
		t.Fatalf("Counter: %v", err)
	}
	if err := r.SaveOffer(ctx, active); err != nil {
		t.Fatalf("SaveOffer: %v", err)
	}
	reread, err := r.FindOffer(ctx, first.ID)
	if err != nil {
		t.Fatalf("FindOffer: %v", err)
	}
	if reread.Total != 90_000 || reread.AuthorID != seller {
		t.Fatalf("offer = %+v, want the seller's revision", reread)
	}

	// Closing it frees the pair, so the buyer can open a new negotiation later.
	if err := reread.Cancel(buyer); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if err := r.SaveOffer(ctx, reread); err != nil {
		t.Fatalf("SaveOffer: %v", err)
	}
	again, err := domain.NewOffer(500, 501, buyer, buyer, seller, 1, 75_000, "retry", 48*time.Hour)
	if err != nil {
		t.Fatalf("NewOffer: %v", err)
	}
	if err := r.InsertOffer(ctx, &again); err != nil {
		t.Fatalf("InsertOffer after cancel: %v", err)
	}
}

// The expiry lists are what the sweep reads: a draft holds a frozen price and an offer holds
// standing terms, so both have to lapse on their own.
func TestExpiryLists(t *testing.T) {
	r, pool := newRepo(t)
	ctx := context.Background()
	buyer, seller := party(t)

	d := draft(t, r, buyer, seller)
	if _, err := pool.Exec(ctx, `UPDATE draft_order SET valid_until = $1 WHERE id = $2`,
		time.Now().Add(-time.Minute), d.ID); err != nil {
		t.Fatalf("backdate draft: %v", err)
	}
	o, err := domain.NewOffer(500, 502, buyer, buyer, seller, 1, 80_000, "bundle", 48*time.Hour)
	if err != nil {
		t.Fatalf("NewOffer: %v", err)
	}
	if err := r.InsertOffer(ctx, &o); err != nil {
		t.Fatalf("InsertOffer: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE offer SET expires_at = $1 WHERE id = $2`,
		time.Now().Add(-time.Minute), o.ID); err != nil {
		t.Fatalf("backdate offer: %v", err)
	}

	drafts, err := r.ExpiredDrafts(ctx, time.Now(), 200)
	if err != nil {
		t.Fatalf("ExpiredDrafts: %v", err)
	}
	if !containsDraft(drafts, d.ID) {
		t.Errorf("expired drafts missing the stale session")
	}
	offers, err := r.ExpiredOffers(ctx, time.Now(), 200)
	if err != nil {
		t.Fatalf("ExpiredOffers: %v", err)
	}
	if !containsOffer(offers, o.ID) {
		t.Errorf("expired offers missing the stale negotiation")
	}

	// Once each has lapsed it leaves the list, so a retried sweep does no work twice.
	if err := d.Cancel(); err != nil {
		t.Fatalf("Cancel draft: %v", err)
	}
	if err := r.SaveDraft(ctx, d); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}
	if err := o.Expire(); err != nil {
		t.Fatalf("Expire offer: %v", err)
	}
	if err := r.SaveOffer(ctx, o); err != nil {
		t.Fatalf("SaveOffer: %v", err)
	}
	drafts, err = r.ExpiredDrafts(ctx, time.Now(), 200)
	if err != nil {
		t.Fatalf("ExpiredDrafts: %v", err)
	}
	if containsDraft(drafts, d.ID) {
		t.Errorf("a cancelled draft is still on the expiry list")
	}
	offers, err = r.ExpiredOffers(ctx, time.Now(), 200)
	if err != nil {
		t.Fatalf("ExpiredOffers: %v", err)
	}
	if containsOffer(offers, o.ID) {
		t.Errorf("an expired offer is still on the expiry list")
	}
}

// The cart is keyed by (account, variant), so adding the same variant twice tops the
// quantity up rather than stacking a second row.
func TestCart_UpsertTopsUp(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	buyer, _ := party(t)

	c, err := domain.NewCartItem(buyer, 500, 501, 2)
	if err != nil {
		t.Fatalf("NewCartItem: %v", err)
	}
	if err := r.UpsertCartItem(ctx, &c); err != nil {
		t.Fatalf("UpsertCartItem: %v", err)
	}
	again, err := domain.NewCartItem(buyer, 500, 501, 3)
	if err != nil {
		t.Fatalf("NewCartItem: %v", err)
	}
	if err := r.UpsertCartItem(ctx, &again); err != nil {
		t.Fatalf("second UpsertCartItem: %v", err)
	}
	if again.ID != c.ID {
		t.Fatalf("ids = %d and %d, want the same row", c.ID, again.ID)
	}
	items, err := r.ListCartItems(ctx, buyer)
	if err != nil {
		t.Fatalf("ListCartItems: %v", err)
	}
	if len(items) != 1 || items[0].Quantity != 5 {
		t.Fatalf("cart = %+v, want one line of 5", items)
	}

	if err := r.DeleteCartItem(ctx, c.ID, buyer); err != nil {
		t.Fatalf("DeleteCartItem: %v", err)
	}
	if _, err := r.FindCartItem(ctx, c.ID, buyer); !errors.Is(err, domain.ErrCartItemNotFound) {
		t.Fatalf("FindCartItem after delete = %v, want ErrCartItemNotFound", err)
	}
}

// A seller's retry list: lines the money has paid for that no order covers yet. It is what
// makes a lost webhook recoverable rather than a sale nobody can see.
func TestListItems_PendingOnly(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	buyer, seller := party(t)
	d := draft(t, r, buyer, seller)
	pending := paidItem(t, r, domain.FromDraft(d.ID), buyer, seller, time.Now().UnixNano()%1_000_000_000)

	items, err := r.ListItems(ctx, port.ItemFilter{
		SellerID: seller, PendingOnly: true, Cursor: port.CursorFilter{Limit: 50},
	})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 1 || items[0].ID != pending.ID {
		t.Fatalf("items = %+v, want the unordered line", items)
	}

	transportID, err := r.InsertTransport(ctx, "ghn-express")
	if err != nil {
		t.Fatalf("InsertTransport: %v", err)
	}
	o, err := domain.NewOrder(domain.FromDraft(d.ID), buyer, seller, transportID, address(), address())
	if err != nil {
		t.Fatalf("NewOrder: %v", err)
	}
	if err := r.CreateOrder(ctx, &o, []int64{pending.ID}); err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	items, err = r.ListItems(ctx, port.ItemFilter{
		SellerID: seller, PendingOnly: true, Cursor: port.CursorFilter{Limit: 50},
	})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("items = %+v, want the list empty once an order covers the line", items)
	}
}

// The dispute queue, and one round ruled once: the unique index on (refund, round) is what
// stops a second verdict on the same argument.
func TestDisputes_OneRoundOneVerdict(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	o, _ := placedOrder(t, r)
	ref, err := domain.NewRefund(o.ID, o.BuyerID, "not as described", nil)
	if err != nil {
		t.Fatalf("NewRefund: %v", err)
	}
	if err := r.InsertRefund(ctx, &ref); err != nil {
		t.Fatalf("InsertRefund: %v", err)
	}

	d, err := domain.NewDispute(ref.ID, o.BuyerID, 1, "photos show otherwise")
	if err != nil {
		t.Fatalf("NewDispute: %v", err)
	}
	if err := r.InsertDispute(ctx, &d); err != nil {
		t.Fatalf("InsertDispute: %v", err)
	}
	dup, err := domain.NewDispute(ref.ID, o.BuyerID, 1, "again")
	if err != nil {
		t.Fatalf("NewDispute: %v", err)
	}
	if err := r.InsertDispute(ctx, &dup); !errors.Is(err, domain.ErrDisputeSettled) {
		t.Fatalf("second InsertDispute = %v, want ErrDisputeSettled", err)
	}

	queue, err := r.ListOpenDisputes(ctx, port.CursorFilter{Limit: 200})
	if err != nil {
		t.Fatalf("ListOpenDisputes: %v", err)
	}
	found := false
	for _, q := range queue {
		if q.ID == d.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("queue is missing the open dispute %d", d.ID)
	}

	if err := d.Rule(o.SellerID+100, true, "evidence is clear"); err != nil {
		t.Fatalf("Rule: %v", err)
	}
	if err := r.SaveDispute(ctx, d); err != nil {
		t.Fatalf("SaveDispute: %v", err)
	}
	ruled, err := r.FindDispute(ctx, d.ID)
	if err != nil {
		t.Fatalf("FindDispute: %v", err)
	}
	if ruled.Status != domain.DisputeBuyerWins || ruled.RuledAt == nil {
		t.Fatalf("dispute = %+v, want a recorded verdict", ruled)
	}
	// A ruled round leaves the queue, so a moderator never sees it twice.
	queue, err = r.ListOpenDisputes(ctx, port.CursorFilter{Limit: 200})
	if err != nil {
		t.Fatalf("ListOpenDisputes: %v", err)
	}
	for _, q := range queue {
		if q.ID == d.ID {
			t.Fatalf("a ruled dispute is still in the queue")
		}
	}
}

// An order with no origin, or with both, is refused by the database — the CHECK is what
// makes "where did this sale come from" answerable from the row alone.
func TestOrderOrigin_ExactlyOneIsEnforced(t *testing.T) {
	_, pool := newRepo(t)
	ctx := context.Background()
	const q = `INSERT INTO "order" (draft_id, offer_id, buyer_id, seller_id, transport_id,
	                                address, pickup_address)
	           VALUES ($1, $2, 1, 2, $3, '{}'::jsonb, '{}'::jsonb)`

	transportID, err := orderpg.New(pool).InsertTransport(ctx, "ghn-express")
	if err != nil {
		t.Fatalf("InsertTransport: %v", err)
	}
	if _, err := pool.Exec(ctx, q, nil, nil, transportID); err == nil {
		t.Error("an order with no origin was accepted")
	}
	if _, err := pool.Exec(ctx, q, 1, 1, transportID); err == nil {
		t.Error("an order claiming both origins was accepted")
	}
}

func ids(orders []domain.Order) []int64 {
	out := make([]int64, len(orders))
	for i, o := range orders {
		out[i] = o.ID
	}
	return out
}

func contains(orders []domain.Order, id int64) bool {
	for _, o := range orders {
		if o.ID == id {
			return true
		}
	}
	return false
}

func containsDraft(drafts []domain.Draft, id int64) bool {
	for _, d := range drafts {
		if d.ID == id {
			return true
		}
	}
	return false
}

func containsOffer(offers []domain.Offer, id int64) bool {
	for _, o := range offers {
		if o.ID == id {
			return true
		}
	}
	return false
}
