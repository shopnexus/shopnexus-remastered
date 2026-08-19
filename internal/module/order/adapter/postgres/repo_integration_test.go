//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
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
	i, err := domain.NewItem(domain.NewLine{
		Origin: origin, BuyerID: buyer, SellerID: seller, ListingID: 500, VariantID: 501,
		Address: address(), Currency: "VND", Quantity: 1,
		TransportOption: "ghn-express", Total: 100_000,
	})
	if err != nil {
		t.Fatalf("NewItem: %v", err)
	}
	i.PaymentSessionID = sessionID
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
	transportID, err := r.InsertTransport(ctx, "ghn-express", 15_000)
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

	transportID, err := r.InsertTransport(ctx, "ghn-express", 15_000)
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

	transportID, err := r.InsertTransport(ctx, "ghn-express", 15_000)
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
// holds it back — which is the guard that stops paying a seller money that is being argued over.
func TestPayoutDue_HeldBackByALiveRefund(t *testing.T) {
	r, pool := newRepo(t)
	ctx := context.Background()
	o, _ := placedOrder(t, r)

	// The seller has to accept the sale before there is anything to receive.
	if err := o.Confirm(); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
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

	// The seller will not grant it, which under this lifecycle means handing the case to staff
	// rather than deciding it themselves — so nothing closes and the payout stays held.
	if err := refund.Escalate(); err != nil {
		t.Fatalf("Escalate: %v", err)
	}
	if err := r.SaveRefund(ctx, refund, domain.RefundAwaitingSeller); err != nil {
		t.Fatalf("SaveRefund: %v", err)
	}
	due, err = r.PayoutDue(ctx, time.Now(), 100)
	if err != nil {
		t.Fatalf("PayoutDue: %v", err)
	}
	if contains(due, o.ID) {
		t.Fatalf("PayoutDue = %v, want it held while staff hold the case", ids(due))
	}

	// Staff decide for the seller. That is what closes the case, and the money is the seller's.
	if err := refund.Resolve(false); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if err := r.SaveRefund(ctx, refund, domain.RefundDisputed); err != nil {
		t.Fatalf("SaveRefund: %v", err)
	}
	due, err = r.PayoutDue(ctx, time.Now(), 100)
	if err != nil {
		t.Fatalf("PayoutDue: %v", err)
	}
	if !contains(due, o.ID) {
		t.Fatalf("PayoutDue = %v, want it due again once the refund closed", ids(due))
	}

	// Paying it out takes it off the list for good, and it is ClaimPayout that writes the
	// completion — under the advisory lock, because whoever writes it wins the escrow.
	if err := r.ClaimPayout(ctx, &o); err != nil {
		t.Fatalf("ClaimPayout: %v", err)
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

	// The first one is still the live case: the index refused the second rather than replacing it.
	live, err := r.FindRefund(ctx, first.ID)
	if err != nil {
		t.Fatalf("FindRefund: %v", err)
	}
	if live.Status != domain.RefundAwaitingSeller {
		t.Fatalf("refund = %+v, want the first still open", live)
	}
}

// The timeout pass: one query finds every refund whose clock has run out, whichever of the two
// windows it was on, and skips the two states that wait on a carrier or a moderator.
func TestOverdueRefunds_BothWindowsOnePass(t *testing.T) {
	r, pool := newRepo(t)
	ctx := context.Background()
	past := time.Now().Add(-time.Hour)

	// A seller who has not answered, and a seller who has not inspected what came back.
	lapsing, _ := placedOrder(t, r)
	sellerSilent := overdue(t, r, pool, lapsing, past, func(ref *domain.Refund) error { return nil })
	notInspected := overdue(t, r, pool, second(t, r), past, func(ref *domain.Refund) error {
		if err := ref.Accept(); err != nil {
			return err
		}
		// The DB requires a return leg on anything marked returned, same as the service does
		// between accepting and the parcel coming back.
		transportID, err := r.InsertTransport(context.Background(), "ghn-express", 0)
		if err != nil {
			return err
		}
		if err := ref.StartReturn(transportID); err != nil {
			return err
		}
		return ref.MarkReturned()
	})
	// An escalated refund waits on staff, so it carries no deadline and no timer can touch it.
	disputed := overdue(t, r, pool, second(t, r), past, func(ref *domain.Refund) error {
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
		t.Errorf("seller review window = %q, want it overdue", got[sellerSilent])
	}
	if got[notInspected] != domain.RefundReturned {
		t.Errorf("seller inspection window = %q, want it overdue", got[notInspected])
	}
	if _, ok := got[disputed]; ok {
		t.Errorf("a disputed refund is on the timer's list; it waits on staff")
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
	from := ref.Status
	if err := move(&ref); err != nil {
		t.Fatalf("transition: %v", err)
	}
	if err := r.SaveRefund(ctx, ref, from); err != nil {
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

	first, err := domain.NewOffer(domain.NewTerms{ListingID: 500, VariantID: 501, BuyerID: buyer, SellerID: seller, Quantity: 1, Total: 80_000, Reason: "bundle"}, 48*time.Hour)
	if err != nil {
		t.Fatalf("NewOffer: %v", err)
	}
	if err := r.InsertOffer(ctx, &first); err != nil {
		t.Fatalf("InsertOffer: %v", err)
	}
	dup, err := domain.NewOffer(domain.NewTerms{ListingID: 500, VariantID: 501, BuyerID: buyer, SellerID: seller, Quantity: 1, Total: 70_000, Reason: "again"}, 48*time.Hour)
	if err != nil {
		t.Fatalf("NewOffer: %v", err)
	}
	if err := r.InsertOffer(ctx, &dup); !errors.Is(err, domain.ErrOfferAlreadyOpen) {
		t.Fatalf("second InsertOffer = %v, want ErrOfferAlreadyOpen", err)
	}

	active, err := r.FindOffer(ctx, first.ID)
	if err != nil {
		t.Fatalf("FindOffer: %v", err)
	}
	if active.Status != domain.OfferActive || active.Total != 80_000 {
		t.Fatalf("active = %+v, want the first offer still on the table", active)
	}

	// A counter revises the same row, and the terms that come back are the new ones.
	if err := active.Counter(seller, 1, 90_000, 100_000, "firm", time.Now(), 48*time.Hour); err != nil {
		t.Fatalf("Counter: %v", err)
	}
	if err := r.SaveOffer(ctx, active, []string{domain.OfferActive}); err != nil {
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
	if err := r.SaveOffer(ctx, reread, []string{domain.OfferActive}); err != nil {
		t.Fatalf("SaveOffer: %v", err)
	}
	again, err := domain.NewOffer(domain.NewTerms{ListingID: 500, VariantID: 501, BuyerID: buyer, SellerID: seller, Quantity: 1, Total: 75_000, Reason: "retry"}, 48*time.Hour)
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
	o, err := domain.NewOffer(domain.NewTerms{ListingID: 500, VariantID: 502, BuyerID: buyer, SellerID: seller, Quantity: 1, Total: 80_000, Reason: "bundle"}, 48*time.Hour)
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
	if err := r.SaveOffer(ctx, o, []string{domain.OfferActive, domain.OfferAccepted}); err != nil {
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

// The checkout claim, which is the one guard standing between a double-clicked "create order now"
// and two payment sessions on one negotiation. Taken before the session exists, so it is a status
// rather than the session id: the second press has to find the terms already gone.
func TestClaimOfferCheckout_OnlyOnePressWins(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	buyer, seller := party(t)

	o, err := domain.NewOffer(domain.NewTerms{ListingID: 500, VariantID: 503, BuyerID: buyer, SellerID: seller, Quantity: 1, Total: 80_000, Reason: "bundle"}, time.Hour)
	if err != nil {
		t.Fatalf("NewOffer: %v", err)
	}
	if err := r.InsertOffer(ctx, &o); err != nil {
		t.Fatalf("InsertOffer: %v", err)
	}
	// Nothing to claim until the other side has agreed.
	now := time.Now()
	if err := r.ClaimOfferCheckout(ctx, o.ID, now); !errors.Is(err, domain.ErrOfferSettled) {
		t.Fatalf("claiming an active offer = %v, want ErrOfferSettled", err)
	}
	if err := o.Accept(seller, now, 30*time.Minute); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if err := r.SaveOffer(ctx, o, []string{domain.OfferActive}); err != nil {
		t.Fatalf("SaveOffer: %v", err)
	}

	if err := r.ClaimOfferCheckout(ctx, o.ID, now); err != nil {
		t.Fatalf("ClaimOfferCheckout: %v", err)
	}
	// The second press finds the terms off the table, before it has opened anything.
	if err := r.ClaimOfferCheckout(ctx, o.ID, now); !errors.Is(err, domain.ErrOfferSettled) {
		t.Fatalf("second claim = %v, want ErrOfferSettled", err)
	}
	claimed, err := r.FindOffer(ctx, o.ID)
	if err != nil {
		t.Fatalf("FindOffer: %v", err)
	}
	if claimed.Status != domain.OfferCheckedOut || claimed.PaymentSessionID != nil {
		t.Fatalf("offer = %+v, want it claimed with no session yet", claimed)
	}
	// A checked-out offer is the session's business, so the expiry sweep leaves it alone even
	// once its own deadline has passed.
	offers, err := r.ExpiredOffers(ctx, now.Add(time.Hour), 200)
	if err != nil {
		t.Fatalf("ExpiredOffers: %v", err)
	}
	if containsOffer(offers, o.ID) {
		t.Errorf("the sweep would expire an offer the buyer is paying for")
	}

	// The claim can be handed back when the checkout could not be opened, and then retried.
	if err := r.ReleaseOfferCheckout(ctx, o.ID); err != nil {
		t.Fatalf("ReleaseOfferCheckout: %v", err)
	}
	if err := r.ClaimOfferCheckout(ctx, o.ID, now); err != nil {
		t.Fatalf("re-claim after release: %v", err)
	}
	if err := r.AttachOfferSession(ctx, o.ID, 90_001); err != nil {
		t.Fatalf("AttachOfferSession: %v", err)
	}
	// Once a session is named the claim is no longer releasable: that money is being paid.
	if err := r.ReleaseOfferCheckout(ctx, o.ID); err != nil {
		t.Fatalf("ReleaseOfferCheckout after attach: %v", err)
	}
	settled, err := r.FindOffer(ctx, o.ID)
	if err != nil {
		t.Fatalf("FindOffer: %v", err)
	}
	if settled.Status != domain.OfferCheckedOut || settled.PaymentSessionID == nil || *settled.PaymentSessionID != 90_001 {
		t.Fatalf("offer = %+v, want it naming its checkout", settled)
	}
	// And an accepted price nobody checked out does lapse — the other half of the same sweep.
	other, err := domain.NewOffer(domain.NewTerms{ListingID: 500, VariantID: 504, BuyerID: buyer, SellerID: seller, Quantity: 1, Total: 60_000, Reason: ""}, time.Hour)
	if err != nil {
		t.Fatalf("NewOffer: %v", err)
	}
	if err := r.InsertOffer(ctx, &other); err != nil {
		t.Fatalf("InsertOffer: %v", err)
	}
	if err := other.Accept(seller, now, 30*time.Minute); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if err := r.SaveOffer(ctx, other, []string{domain.OfferActive}); err != nil {
		t.Fatalf("SaveOffer: %v", err)
	}
	offers, err = r.ExpiredOffers(ctx, now.Add(time.Hour), 200)
	if err != nil {
		t.Fatalf("ExpiredOffers: %v", err)
	}
	if !containsOffer(offers, other.ID) {
		t.Errorf("an agreed price nobody checked out never lapses")
	}
}

// The booking marker lives in the carrier's own payload, so the retry list is a JSON predicate
// rather than a column — which is exactly the kind of SQL a fake cannot check.
func TestBookTransport_MarksTheParcelAndLeavesTheRetryList(t *testing.T) {
	r, pool := newRepo(t)
	ctx := context.Background()
	// The retry list is bounded and oldest-first, so earlier runs' parcels are marked booked
	// first: without that this order sits behind hundreds of them and the assertion is about the
	// batch size rather than about the predicate.
	if _, err := pool.Exec(ctx, `UPDATE transport SET data = '{"provider_ref":"drained"}'
	                             WHERE status = 'pending' AND data->>'provider_ref' IS NULL`); err != nil {
		t.Fatalf("drain the retry list: %v", err)
	}
	o, _ := placedOrder(t, r)

	// A parcel on a sale the seller has not accepted is nobody's to book: the money created the
	// order, not the shipment, so the retry list must not hand an unposted parcel to a courier.
	unbooked, err := r.UnbookedTransports(ctx, time.Now().Add(time.Minute), 200)
	if err != nil {
		t.Fatalf("UnbookedTransports: %v", err)
	}
	if slices.Contains(unbooked, o.ID) {
		t.Fatalf("unbooked = %v, want an unconfirmed sale left alone", unbooked)
	}

	// The seller accepting is what lets the parcel be booked, so a confirmed order whose carrier
	// never heard about it is on the list, once it is past the grace period.
	if err := o.Confirm(); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if err := r.SaveOrder(ctx, o); err != nil {
		t.Fatalf("SaveOrder: %v", err)
	}
	unbooked, err = r.UnbookedTransports(ctx, time.Now().Add(time.Minute), 200)
	if err != nil {
		t.Fatalf("UnbookedTransports: %v", err)
	}
	if !slices.Contains(unbooked, o.ID) {
		t.Fatalf("unbooked = %v, want the confirmed order whose parcel no carrier took", unbooked)
	}
	// And not before it: a booking still in flight must not be raced.
	fresh, err := r.UnbookedTransports(ctx, time.Now().Add(-time.Hour), 200)
	if err != nil {
		t.Fatalf("UnbookedTransports before: %v", err)
	}
	if slices.Contains(fresh, o.ID) {
		t.Errorf("a shipment younger than the grace period is already on the retry list")
	}

	ref := fmt.Sprintf("trk-%d", o.ID)
	if err := r.BookTransport(ctx, o.TransportID, []byte(`{"provider_ref":"`+ref+`","provider_data":{"label":"x"}}`)); err != nil {
		t.Fatalf("BookTransport: %v", err)
	}
	booked, err := r.FindTransport(ctx, o.TransportID)
	if err != nil {
		t.Fatalf("FindTransport: %v", err)
	}
	if !booked.Booked() {
		t.Fatalf("transport = %+v, want it to carry the carrier's reference", booked)
	}
	// Off the list, so a retry pass does not book a second parcel for one sale.
	unbooked, err = r.UnbookedTransports(ctx, time.Now().Add(time.Minute), 200)
	if err != nil {
		t.Fatalf("UnbookedTransports after booking: %v", err)
	}
	if slices.Contains(unbooked, o.ID) {
		t.Errorf("a booked parcel is still on the retry list")
	}
	// A second booking is refused rather than replacing a reference the carrier is holding.
	if err := r.BookTransport(ctx, o.TransportID, []byte(`{"provider_ref":"trk-other"}`)); !errors.Is(err, domain.ErrTransportSettled) {
		t.Fatalf("second BookTransport = %v, want ErrTransportSettled", err)
	}

	// The webhook finds it by the courier's id, which is the only one a courier knows.
	found, err := r.FindTransportByRef(ctx, ref)
	if err != nil {
		t.Fatalf("FindTransportByRef: %v", err)
	}
	if found.ID != o.TransportID {
		t.Fatalf("found transport %d, want %d", found.ID, o.TransportID)
	}
	if _, err := r.FindTransportByRef(ctx, "trk-nobody"); !errors.Is(err, domain.ErrTransportNotFound) {
		t.Fatalf("unknown ref = %v, want ErrTransportNotFound", err)
	}
}

// The cart is keyed by (account, variant), so adding the same variant twice tops the// The cart is keyed by (account, variant), so adding the same variant twice tops the
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

	transportID, err := r.InsertTransport(ctx, "ghn-express", 15_000)
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

// A verdict and the refund it moved are one write, and the guard is the refund's own status:
// a case staff were never asked about cannot be decided, and a decided one cannot be re-decided.
func TestSaveRefundOutcome_VerdictNeedsALiveCase(t *testing.T) {
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
	// A verdict is only reached on a case somebody escalated.
	if err := ref.Resolve(true); !errors.Is(err, domain.ErrRefundNotDisputed) {
		t.Fatalf("Resolve before escalation = %v, want ErrRefundNotDisputed", err)
	}
	if err := ref.Escalate(); err != nil {
		t.Fatalf("Escalate: %v", err)
	}
	// Two in-memory moves, one write: `from` is where the row still is, not where the entity has
	// been.
	if err := r.SaveRefund(ctx, ref, domain.RefundAwaitingSeller); err != nil {
		t.Fatalf("SaveRefund: %v", err)
	}

	// The buyer wins with the goods still theirs, so the refund is granted and the parcel is
	// what settles it later — the return leg has to exist before the write.
	if err := ref.Resolve(true); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	legID, err := r.InsertTransport(ctx, "ghn-express", 0)
	if err != nil {
		t.Fatalf("InsertTransport: %v", err)
	}
	if err := ref.StartReturn(legID); err != nil {
		t.Fatalf("StartReturn: %v", err)
	}
	if err := r.SaveRefundOutcome(ctx, ref, nil, domain.RefundDisputed); err != nil {
		t.Fatalf("SaveRefundOutcome: %v", err)
	}
	granted, err := r.FindRefund(ctx, ref.ID)
	if err != nil {
		t.Fatalf("FindRefund: %v", err)
	}
	if granted.Status != domain.RefundReturning || granted.ReturnTransportID == nil ||
		granted.DeadlineAt != nil {
		t.Fatalf("refund = %+v, want it returning with a leg and no clock", granted)
	}
	// A write whose `from` is not where the row is loses even though the case is still live: that is
	// what keeps the overdue sweep from settling a refund somebody escalated while the pass was
	// working through earlier rows.
	stale := granted
	stale.Status = domain.RefundAccepted
	if err := r.SaveRefund(ctx, stale, domain.RefundReturned); !errors.Is(err, domain.ErrRefundSettled) {
		t.Fatalf("stale SaveRefund = %v, want the guard to refuse a write from the wrong status", err)
	}

	// The goods arrive and nobody contests them, so the money goes back and the order closes with
	// it: an accepted refund over an order the payout sweep can still see is money paid twice.
	if err := granted.MarkReturned(); err != nil {
		t.Fatalf("MarkReturned: %v", err)
	}
	if err := granted.Settle(); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	closed := o
	if err := closed.Cancel(false); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if err := r.SaveRefundOutcome(ctx, granted, &closed, domain.RefundReturning); err != nil {
		t.Fatalf("SaveRefundOutcome: %v", err)
	}
	// A settled case is not movable again, which is the status guard in the UPDATE.
	if err := r.SaveRefundOutcome(ctx, granted, nil, domain.RefundReturning); !errors.Is(err, domain.ErrRefundSettled) {
		t.Fatalf("second SaveRefundOutcome = %v, want ErrRefundSettled", err)
	}
	settledOrder, err := r.FindOrder(ctx, o.ID)
	if err != nil {
		t.Fatalf("FindOrder: %v", err)
	}
	if !settledOrder.Settled() {
		t.Fatalf("order = %q, want it closed with the refund", settledOrder.State())
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

	transportID, err := orderpg.New(pool).InsertTransport(ctx, "ghn-express", 15_000)
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

// The escrow has one claimant. `PayoutDue`'s "no live refund" is a read, and so is the check a
// refund makes about the order still being open: under READ COMMITTED each statement sees a world
// where its own write is legal, so both used to land — the sweep released the money to the seller
// and the verdict then refunded the buyer out of a hold that was gone. The advisory lock both
// sides take is what serialises them.
//
// Concurrently, with the refund's insert held open inside a transaction that has the lock: the
// payout has to block on it and then find the refund it was going to step over.
func TestPayoutAndRefund_CannotBothClaimTheEscrow(t *testing.T) {
	r, pool := newRepo(t)
	ctx := context.Background()
	o, _ := placedOrder(t, r)
	if err := o.Confirm(); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if err := r.SaveOrder(ctx, o); err != nil {
		t.Fatalf("SaveOrder: %v", err)
	}
	if err := o.ConfirmReceipt([]int64{1}); err != nil {
		t.Fatalf("ConfirmReceipt: %v", err)
	}
	if err := r.SaveOrder(ctx, o); err != nil {
		t.Fatalf("SaveOrder: %v", err)
	}
	// Wind the receipt back so the window has passed and the order is a payout candidate.
	if _, err := pool.Exec(ctx,
		`UPDATE "order" SET received_at = received_at - $1::interval WHERE id = $2`,
		"96 hours", o.ID); err != nil {
		t.Fatalf("age receipt: %v", err)
	}
	due, err := r.PayoutDue(ctx, time.Now(), 200)
	if err != nil {
		t.Fatalf("PayoutDue: %v", err)
	}
	if !contains(due, o.ID) {
		t.Fatalf("the order is not a payout candidate: %v", ids(due))
	}

	// A refund lands in the gap between that select and the write it was going to make.
	ref, err := domain.NewRefund(o.ID, o.BuyerID, "not as described", nil)
	if err != nil {
		t.Fatalf("NewRefund: %v", err)
	}
	if err := r.InsertRefund(ctx, &ref); err != nil {
		t.Fatalf("InsertRefund: %v", err)
	}
	claimed := o
	if err := r.ClaimPayout(ctx, &claimed); !errors.Is(err, domain.ErrOrderSettled) {
		t.Fatalf("ClaimPayout = %v, want ErrOrderSettled; the seller was paid over a live refund", err)
	}

	// And the other way round: an order the payout has claimed has no escrow left to argue over.
	o2, _ := placedOrder(t, r)
	if err := o2.Confirm(); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if err := r.SaveOrder(ctx, o2); err != nil {
		t.Fatalf("SaveOrder: %v", err)
	}
	if err := o2.ConfirmReceipt([]int64{1}); err != nil {
		t.Fatalf("ConfirmReceipt: %v", err)
	}
	if err := r.SaveOrder(ctx, o2); err != nil {
		t.Fatalf("SaveOrder: %v", err)
	}
	if err := r.ClaimPayout(ctx, &o2); err != nil {
		t.Fatalf("ClaimPayout: %v", err)
	}
	late, err := domain.NewRefund(o2.ID, o2.BuyerID, "too late", nil)
	if err != nil {
		t.Fatalf("NewRefund: %v", err)
	}
	if err := r.InsertRefund(ctx, &late); !errors.Is(err, domain.ErrRefundNotDue) {
		t.Fatalf("InsertRefund = %v, want ErrRefundNotDue over a paid-out order", err)
	}

	// The claimed order is what RetryClaimedPayouts finds, since nothing else would ever try the
	// release again.
	retry, err := r.ClaimedPayouts(ctx, 200)
	if err != nil {
		t.Fatalf("ClaimedPayouts: %v", err)
	}
	if !contains(retry, o2.ID) {
		t.Fatalf("claimed payouts = %v, want the order whose release may not have landed", ids(retry))
	}
	// And recording the release takes it off that list for good — which is what stops the retry
	// pass asking finance about every sale the platform has ever completed.
	o2.MarkPayoutReleased()
	if err := r.MarkPayoutReleased(ctx, o2); err != nil {
		t.Fatalf("MarkPayoutReleased: %v", err)
	}
	retry, err = r.ClaimedPayouts(ctx, 200)
	if err != nil {
		t.Fatalf("ClaimedPayouts: %v", err)
	}
	if contains(retry, o2.ID) {
		t.Fatalf("claimed payouts = %v, want a released order gone", ids(retry))
	}
	reread, err := r.FindOrder(ctx, o2.ID)
	if err != nil {
		t.Fatalf("FindOrder: %v", err)
	}
	if reread.PayoutReleasedAt == nil {
		t.Fatal("the release time was not stored")
	}
}

// A settlement resumed after the order was written finishes the linking it left behind. The real
// CreateOrder cannot do it: it re-runs the INSERT, loses on the origin, and the rollback takes the
// link with it — which used to read as success.
func TestLinkItems_FinishesAResumedSettlement(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	buyer, seller := party(t)
	d := draft(t, r, buyer, seller)
	session := time.Now().UnixNano() % 1_000_000_000
	first := paidItem(t, r, domain.FromDraft(d.ID), buyer, seller, session)
	transportID, err := r.InsertTransport(ctx, "ghn-express", 15_000)
	if err != nil {
		t.Fatalf("InsertTransport: %v", err)
	}
	o, err := domain.NewOrder(domain.FromDraft(d.ID), buyer, seller, transportID, address(), address())
	if err != nil {
		t.Fatalf("NewOrder: %v", err)
	}
	if err := r.CreateOrder(ctx, &o, []int64{first.ID}); err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	// A second line of the same checkout that the first attempt never got to.
	second := paidItem(t, r, domain.FromDraft(d.ID), buyer, seller, session)

	// What a resumed settlement used to do, and why it silently did nothing.
	retry := o
	retry.ID = 0
	if err := r.CreateOrder(ctx, &retry, []int64{second.ID}); !errors.Is(err, domain.ErrOrderSettled) {
		t.Fatalf("CreateOrder = %v, want ErrOrderSettled on the origin", err)
	}
	items, err := r.OrderItems(ctx, o.ID)
	if err != nil {
		t.Fatalf("OrderItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want the rolled-back link to have changed nothing", len(items))
	}

	if err := r.LinkItems(ctx, o.ID, []int64{first.ID, second.ID}); err != nil {
		t.Fatalf("LinkItems: %v", err)
	}
	items, err = r.OrderItems(ctx, o.ID)
	if err != nil {
		t.Fatalf("OrderItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want both lines on the order", len(items))
	}
	// Again claims nothing: a line an order already covers is left where it is.
	if err := r.LinkItems(ctx, o.ID, []int64{first.ID, second.ID}); err != nil {
		t.Fatalf("second LinkItems: %v", err)
	}
}

// The cursor is the (created_at, id) pair. Three lines written in one transaction share
// `created_at` to the microsecond — CURRENT_TIMESTAMP is transaction-scoped — so a bound on the
// timestamp alone put the rest of the group out of reach: page 2 came back empty and the third
// line was unreachable.
func TestListItems_CursorReachesRowsSharingATimestamp(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	buyer, seller := party(t)
	d := draft(t, r, buyer, seller)
	session := time.Now().UnixNano() % 1_000_000_000
	lines := make([]*domain.Item, 0, 3)
	for range 3 {
		i, err := domain.NewItem(domain.NewLine{
			Origin: domain.FromDraft(d.ID), BuyerID: buyer, SellerID: seller,
			ListingID: 500, VariantID: 501, Address: address(), Currency: "VND", Quantity: 1,
			TransportOption: "ghn-express", Total: 100_000,
		})
		if err != nil {
			t.Fatalf("NewItem: %v", err)
		}
		i.PaymentSessionID = session
		lines = append(lines, &i)
	}
	if err := r.InsertItems(ctx, lines); err != nil {
		t.Fatalf("InsertItems: %v", err)
	}
	at := lines[0].CreatedAt
	for _, i := range lines[1:] {
		if !i.CreatedAt.Equal(at) {
			t.Skipf("the three lines did not share a timestamp (%v vs %v)", at, i.CreatedAt)
		}
	}

	seen := map[int64]bool{}
	cursor := port.CursorFilter{Limit: 2}
	for range 5 {
		page, err := r.ListItems(ctx, port.ItemFilter{BuyerID: buyer, Cursor: cursor})
		if err != nil {
			t.Fatalf("ListItems: %v", err)
		}
		if len(page) == 0 {
			break
		}
		for _, i := range page {
			seen[i.ID] = true
		}
		last := page[len(page)-1]
		cursor = port.CursorFilter{Before: last.CreatedAt, BeforeID: last.ID, Limit: 2}
	}
	if len(seen) != 3 {
		t.Fatalf("saw %d of 3 lines, want every row reachable: %v", len(seen), seen)
	}
}

// A summary is two statements over two grains — orders and their lines — and the fake repository can
// only claim they agree. Here they have to: a cancelled line is not revenue, a delivery fee is not on
// a line at all, and the buckets are cut on a local date rather than on UTC.
func TestOrderSummary_CountsOrdersAndMoneySeparately(t *testing.T) {
	r, pool := newRepo(t)
	ctx := context.Background()
	o, item := placedOrder(t, r)

	filter := port.SummaryFilter{
		SellerID: o.SellerID,
		From:     time.Now().Add(-time.Hour),
		To:       time.Now().Add(time.Hour),
		TZ:       "Asia/Ho_Chi_Minh",
	}
	counts, err := r.CountOrders(ctx, filter)
	if err != nil {
		t.Fatalf("CountOrders: %v", err)
	}
	if counts.Open != 1 || counts.Completed != 0 || counts.Cancelled != 0 {
		t.Fatalf("counts = %+v, want the one open order", counts)
	}
	if len(counts.Totals) != 0 {
		t.Fatalf("totals = %+v, want nothing while the order is open", counts.Totals)
	}

	// Complete it the way the payout does — `completed_at` is written by the claim and nothing else —
	// then the goods are revenue, and only the goods: the order's transport carries a 15,000 fee that
	// must not appear here.
	if err := o.Confirm(); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if err := r.SaveOrder(ctx, o); err != nil {
		t.Fatalf("SaveOrder: %v", err)
	}
	if err := o.ConfirmReceipt([]int64{item.ID}); err != nil {
		t.Fatalf("ConfirmReceipt: %v", err)
	}
	if err := r.SaveOrder(ctx, o); err != nil {
		t.Fatalf("SaveOrder: %v", err)
	}
	if err := r.ClaimPayout(ctx, &o); err != nil {
		t.Fatalf("ClaimPayout: %v", err)
	}
	counts, err = r.CountOrders(ctx, filter)
	if err != nil {
		t.Fatalf("CountOrders: %v", err)
	}
	if counts.Open != 0 || counts.Completed != 1 {
		t.Fatalf("counts = %+v, want it completed", counts)
	}
	if counts.Totals["VND"] != 100_000 {
		t.Fatalf("totals = %+v, want the line's goods without the carriage", counts.Totals)
	}

	// A cancelled line stops being revenue while the order it belongs to stays completed.
	if _, err := pool.Exec(ctx, `UPDATE item SET cancelled_at = now() WHERE id = $1`, item.ID); err != nil {
		t.Fatalf("cancel line: %v", err)
	}
	counts, err = r.CountOrders(ctx, filter)
	if err != nil {
		t.Fatalf("CountOrders: %v", err)
	}
	if counts.Completed != 1 || len(counts.Totals) != 0 {
		t.Fatalf("counts = %+v, want a completed order whose cancelled line earns nothing", counts)
	}

	days, err := r.ListOrderDays(ctx, filter)
	if err != nil {
		t.Fatalf("ListOrderDays: %v", err)
	}
	if len(days) != 1 || days[0].Placed != 1 || days[0].Completed != 1 {
		t.Fatalf("days = %+v, want one bucket for the one order", days)
	}
	// The bucket is the seller's date, not the server's: an order placed at 23:30 in Saigon is that
	// day's, and reading it as UTC would file it under the day before.
	local := time.Now().In(time.FixedZone("ICT", 7*60*60)).Format("2006-01-02")
	if days[0].Date != local {
		t.Fatalf("bucket = %q, want the local date %q", days[0].Date, local)
	}

	// A window that ends before the order was placed holds nothing — `to` is exclusive.
	empty, err := r.CountOrders(ctx, port.SummaryFilter{
		SellerID: o.SellerID, From: filter.From.Add(-48 * time.Hour), To: filter.From, TZ: "UTC",
	})
	if err != nil {
		t.Fatalf("CountOrders(empty window): %v", err)
	}
	if empty.Open != 0 || empty.Completed != 0 {
		t.Fatalf("counts = %+v, want an empty window", empty)
	}
}
