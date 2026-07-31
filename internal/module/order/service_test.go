package order_test

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"testing"

	"shopnexus/internal/infra/eventbus"
	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/api/accounttest"
	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/module/catalog/api/catalogtest"
	chatapi "shopnexus/internal/module/chat/api"
	"shopnexus/internal/module/chat/api/chattest"
	financeapi "shopnexus/internal/module/finance/api"
	"shopnexus/internal/module/finance/api/financetest"
	"shopnexus/internal/module/order"
	orderapi "shopnexus/internal/module/order/api"
	"shopnexus/internal/module/order/domain"
	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/id/idtest"
	"shopnexus/internal/shared/validation"
)

func TestMain(m *testing.M) { idtest.Install(); m.Run() }

const (
	buyer  = id.ID[id.Account](1)
	seller = id.ID[id.Account](2)
	admin  = id.ID[id.Account](3)
	// The listing and the variant every test buys.
	listingID = id.ID[id.Listing](10)
	variantID = id.ID[id.Variant](11)
	contactID = id.ID[id.Contact](12)
)

// fakeAccounts answers the three questions order asks of the account module: the caller's
// role, a party's name, and an address to ship to or collect from.
type fakeAccounts struct {
	accounttest.Stub
	role string
	// noPickup is a seller who never set a collection point, which stops a sale rather than
	// guessing an address.
	noPickup bool
}

func (f fakeAccounts) GetMe(context.Context, accountapi.GetMeRequest) (accountapi.Me, error) {
	return accountapi.Me{Role: f.role}, nil
}

func (f fakeAccounts) GetPublicAccount(_ context.Context, req accountapi.GetPublicAccountRequest) (accountapi.PublicAccount, error) {
	return accountapi.PublicAccount{ID: req.ID, Name: "Somebody", IdentityVerified: true}, nil
}

func (f fakeAccounts) GetContact(_ context.Context, req accountapi.GetContactRequest) (accountapi.Contact, error) {
	return accountapi.Contact{ID: req.ID, FullName: "Buyer", Phone: "+84900000001", Country: "VN"}, nil
}

func (f fakeAccounts) GetPickupContact(_ context.Context, req accountapi.GetPickupContactRequest) (accountapi.Contact, error) {
	if f.noPickup {
		return accountapi.Contact{}, errx.NewError(422, "no_pickup_contact", "no pickup address")
	}
	return accountapi.Contact{FullName: "Seller", Phone: "+84900000002", Country: "VN"}, nil
}

// fakeCatalog answers what a listing costs and holds its stock. The counters are real, so a
// test can see a reservation happen and come back.
type fakeCatalog struct {
	catalogtest.Stub
	priceMode string
	price     int64
	// reserved and sold are what the stock movements did, which is how a test checks that a
	// cancelled line gave its units back.
	reserved int64
	sold     int64
	// available bounds a reservation, so an oversell is refusable.
	available int64
}

func (f *fakeCatalog) GetListing(_ context.Context, req catalogapi.GetListingRequest) (catalogapi.ListingDetail, error) {
	return f.listing(), nil
}

func (f *fakeCatalog) ListListings(context.Context, catalogapi.ListListingsRequest) (catalogapi.ListingPage, error) {
	return catalogapi.ListingPage{Data: []catalogapi.Listing{{ID: listingID}}}, nil
}

func (f *fakeCatalog) listing() catalogapi.ListingDetail {
	return catalogapi.ListingDetail{
		ID: listingID, Name: "Ao thun", Currency: "VND", PriceMode: f.priceMode,
		Seller:   accountapi.AccountSummary{ID: seller},
		Variants: []catalogapi.Variant{{ID: variantID, Price: f.price}},
	}
}

func (f *fakeCatalog) ReserveStock(_ context.Context, req catalogapi.StockMovementRequest) error {
	if f.reserved+f.sold+req.Units > f.available {
		return errx.NewError(409, "insufficient_stock", "not enough stock")
	}
	f.reserved += req.Units
	return nil
}

func (f *fakeCatalog) ReleaseStock(_ context.Context, req catalogapi.StockMovementRequest) error {
	f.reserved -= req.Units
	return nil
}

func (f *fakeCatalog) CommitStock(_ context.Context, req catalogapi.StockMovementRequest) error {
	f.reserved -= req.Units
	f.sold += req.Units
	return nil
}

// fakeFinance is the money. It records what was held, released and refunded, which is what a
// test asserts about — the amounts, not the ledger.
type fakeFinance struct {
	financetest.Stub
	nextSession int64
	held        int64
	released    int64
	refunded    int64
	// posted is the idempotency index: a key used twice is refused, as the real one does.
	posted map[string]bool
}

func newFakeFinance() *fakeFinance {
	return &fakeFinance{posted: map[string]bool{}}
}

func (f *fakeFinance) OpenCheckout(_ context.Context, req financeapi.OpenCheckoutRequest) (financeapi.Session, error) {
	f.nextSession++
	return financeapi.Session{
		ID: id.Of[id.PaymentSession](f.nextSession), Kind: "buyer-checkout",
		Status: "pending", Currency: req.Currency, TotalAmount: req.Total,
		Outstanding: req.Total,
	}, nil
}

func (f *fakeFinance) HoldEscrow(_ context.Context, req financeapi.EscrowRequest) error {
	if f.posted[req.IdempotencyKey] {
		return errx.NewError(409, "movement_already_posted", "already posted")
	}
	f.posted[req.IdempotencyKey] = true
	f.held += req.Amount
	return nil
}

func (f *fakeFinance) ReleaseEscrow(_ context.Context, req financeapi.EscrowRequest) error {
	if f.posted[req.IdempotencyKey] {
		return errx.NewError(409, "movement_already_posted", "already posted")
	}
	f.posted[req.IdempotencyKey] = true
	f.released += req.Amount
	return nil
}

func (f *fakeFinance) RefundEscrow(_ context.Context, req financeapi.EscrowRequest) error {
	if f.posted[req.IdempotencyKey] {
		return errx.NewError(409, "movement_already_posted", "already posted")
	}
	f.posted[req.IdempotencyKey] = true
	f.refunded += req.Amount
	return nil
}

// fakeChat records the cards a negotiation posts, which is the only thing order asks of it.
type fakeChat struct {
	chattest.Stub
	cards []map[string]any
}

func (f *fakeChat) PostSystemMessage(_ context.Context, req chatapi.PostSystemMessageRequest) (chatapi.Message, error) {
	f.cards = append(f.cards, req.Card)
	return chatapi.Message{}, nil
}

type harness struct {
	svc       *order.Service
	repo      *fakeRepo
	catalog   *fakeCatalog
	finance   *fakeFinance
	chat      *fakeChat
	accounts  fakeAccounts
	workflows *fakeWorkflows
}

// fakeWorkflows records the durable timers the service asked for. They are best-effort by
// contract, so what a test checks is that the run was started at all — a checkout with no
// clock on it is reserved stock nobody gives back.
type fakeWorkflows struct {
	// calls is "what, id" in order, which is enough to assert a wait was opened or closed.
	calls []string
}

func (f *fakeWorkflows) record(what string, id int64) error {
	f.calls = append(f.calls, fmt.Sprintf("%s:%d", what, id))
	return nil
}

func (f *fakeWorkflows) saw(want string) bool { return slices.Contains(f.calls, want) }

func (f *fakeWorkflows) StartCheckout(_ context.Context, sessionID int64) error {
	return f.record("start-checkout", sessionID)
}

func (f *fakeWorkflows) CheckoutPaid(_ context.Context, sessionID int64) error {
	return f.record("checkout-paid", sessionID)
}

func (f *fakeWorkflows) CheckoutCancelled(_ context.Context, sessionID int64) error {
	return f.record("checkout-cancelled", sessionID)
}

func (f *fakeWorkflows) StartOrder(_ context.Context, orderID int64) error {
	return f.record("start-order", orderID)
}

func (f *fakeWorkflows) OrderReceived(_ context.Context, orderID int64) error {
	return f.record("order-received", orderID)
}

func (f *fakeWorkflows) RefundRaised(_ context.Context, orderID int64) error {
	return f.record("refund-raised", orderID)
}

func (f *fakeWorkflows) RefundResolved(_ context.Context, orderID int64, buyerPaid bool) error {
	return f.record(fmt.Sprintf("refund-resolved(%t)", buyerPaid), orderID)
}

func (f *fakeWorkflows) StartRefund(_ context.Context, refundID int64) error {
	return f.record("start-refund", refundID)
}

func (f *fakeWorkflows) RefundMoved(_ context.Context, refundID int64) error {
	return f.record("refund-moved", refundID)
}

func newHarness(priceMode string) *harness {
	repo := newFakeRepo()
	catalog := &fakeCatalog{priceMode: priceMode, price: 100_000, available: 5}
	finance := newFakeFinance()
	chat := &fakeChat{}
	accounts := fakeAccounts{role: "user"}
	workflows := &fakeWorkflows{}
	svc := order.NewService(repo, accounts, catalog, finance, chat, repo, workflows,
		eventbus.NewMemory(slog.New(slog.DiscardHandler)), validation.Default(),
		slog.New(slog.DiscardHandler))
	return &harness{svc: svc, repo: repo, catalog: catalog, finance: finance, chat: chat,
		accounts: accounts, workflows: workflows}
}

// moderator reuses one harness's repository with a staff caller.
func (h *harness) moderator() *order.Service {
	return order.NewService(h.repo, fakeAccounts{role: "moderator"}, h.catalog, h.finance,
		h.chat, h.repo, h.workflows, eventbus.NewMemory(slog.New(slog.DiscardHandler)),
		validation.Default(), slog.New(slog.DiscardHandler))
}

func status(t *testing.T, err error) uint16 {
	t.Helper()
	s, _, _, ok := errx.Decompose(err)
	if !ok {
		t.Fatalf("expected a coded error, got %v", err)
	}
	return s
}

func mustErr[T any](_ T, err error) error { return err }

// checkout takes a fixed-price listing all the way to a paid order, which is the path every
// order test needs behind it.
func (h *harness) checkout(t *testing.T) (orderapi.CheckoutResult, orderapi.Order) {
	t.Helper()
	ctx := context.Background()
	draft, err := h.svc.CreateDraft(ctx, orderapi.CreateDraftRequest{
		ActorID: buyer, ListingID: listingID,
	})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	result, err := h.svc.Checkout(ctx, orderapi.CheckoutRequest{
		ActorID: buyer, ID: draft.ID, ContactID: contactID, Currency: "VND",
		TransportOption: "ghn-express",
		Lines:           []orderapi.CheckoutLine{{VariantID: variantID, Quantity: 1}},
	})
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	// The money is what creates the order — no seller step in between.
	if err := h.svc.SettlePaidSession(ctx, result.PaymentSession); err != nil {
		t.Fatalf("SettlePaidSession: %v", err)
	}
	page, err := h.svc.ListOrders(ctx, orderapi.ListOrdersRequest{
		ActorID: buyer, Role: "buyer", Limit: 10,
	})
	if err != nil {
		t.Fatalf("ListOrders: %v", err)
	}
	if len(page.Data) != 1 {
		t.Fatalf("orders = %+v, want the one the money created", page.Data)
	}
	return result, page.Data[0]
}

// The whole fixed-price path: a draft freezes the price, checkout reserves and charges, and
// the settled session writes the order, the shipment and the escrow hold.
func TestCheckout_MoneyCreatesTheOrder(t *testing.T) {
	h := newHarness("fixed")
	result, o := h.checkout(t)

	if result.Total != 100_000 || result.Currency != "VND" {
		t.Fatalf("checkout = %+v, want the frozen price", result)
	}
	if o.State != domain.StateOpen {
		t.Fatalf("state = %q, want open", o.State)
	}
	if o.DraftID == nil || o.OfferID != nil {
		t.Fatalf("order = %+v, want it to name the draft it came from", o)
	}
	if o.Transport == nil || o.Transport.Status != domain.TransportPending {
		t.Fatalf("transport = %+v, want a pending shipment", o.Transport)
	}
	if h.finance.held != 100_000 {
		t.Errorf("held = %d, want the total in escrow", h.finance.held)
	}
	// The reservation became a sale: the units are gone, not merely held.
	if h.catalog.sold != 1 || h.catalog.reserved != 0 {
		t.Errorf("stock = reserved %d sold %d, want it committed", h.catalog.reserved, h.catalog.sold)
	}

	// Every wait this sale needed was opened and closed on time: a checkout with no clock on
	// it is reserved stock nobody gives back, and an order with none is escrow nobody releases.
	for _, want := range []string{
		"start-checkout:1", "checkout-paid:1", fmt.Sprintf("start-order:%d", o.ID.Int64()),
	} {
		if !h.workflows.saw(want) {
			t.Errorf("timer %q was never set; calls = %v", want, h.workflows.calls)
		}
	}

	// A redelivered webhook is a no-op, not a second order: the origin is unique.
	if err := h.svc.SettlePaidSession(context.Background(), result.PaymentSession); err != nil {
		t.Fatalf("second SettlePaidSession: %v", err)
	}
	page, err := h.svc.ListOrders(context.Background(), orderapi.ListOrdersRequest{
		ActorID: buyer, Role: "buyer", Limit: 10,
	})
	if err != nil {
		t.Fatalf("ListOrders: %v", err)
	}
	if len(page.Data) != 1 {
		t.Fatalf("orders = %d, want a redelivery to mint nothing", len(page.Data))
	}
	if h.finance.held != 100_000 {
		t.Errorf("held = %d, want the escrow held once", h.finance.held)
	}
}

// A negotiable listing cannot be bought from the listing page: its price is agreed first.
func TestCreateDraft_RefusesANegotiableListing(t *testing.T) {
	h := newHarness("negotiable")
	err := mustErr(h.svc.CreateDraft(context.Background(), orderapi.CreateDraftRequest{
		ActorID: buyer, ListingID: listingID,
	}))
	if got := status(t, err); got != 422 {
		t.Fatalf("status = %d, want 422", got)
	}
}

// A checkout that would oversell is refused, and nothing is left reserved behind it.
func TestCheckout_RefusesAnOversell(t *testing.T) {
	h := newHarness("fixed")
	h.catalog.available = 0
	ctx := context.Background()
	draft, err := h.svc.CreateDraft(ctx, orderapi.CreateDraftRequest{ActorID: buyer, ListingID: listingID})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	err = mustErr(h.svc.Checkout(ctx, orderapi.CheckoutRequest{
		ActorID: buyer, ID: draft.ID, ContactID: contactID, Currency: "VND",
		TransportOption: "ghn-express",
		Lines:           []orderapi.CheckoutLine{{VariantID: variantID, Quantity: 1}},
	}))
	if got := status(t, err); got != 409 {
		t.Fatalf("status = %d, want 409", got)
	}
	if h.catalog.reserved != 0 {
		t.Errorf("reserved = %d, want nothing held after a refused checkout", h.catalog.reserved)
	}
}

// The negotiation: the two sides alternate, only the buyer closes it, and accepting opens the
// same checkout a fixed-price sale uses.
func TestOffer_NegotiateThenAccept(t *testing.T) {
	h := newHarness("negotiable")
	ctx := context.Background()
	offer, err := h.svc.CreateOffer(ctx, orderapi.CreateOfferRequest{
		ActorID: buyer, VariantID: variantID, Quantity: 1, Total: 80_000, Reason: "bundle",
	})
	if err != nil {
		t.Fatalf("CreateOffer: %v", err)
	}
	// The card went into the thread, carrying the offer's id and nothing else.
	if len(h.chat.cards) != 1 || h.chat.cards[0]["offer_id"] == nil {
		t.Fatalf("cards = %+v, want one carrying the offer id", h.chat.cards)
	}
	if h.chat.cards[0]["total"] != nil {
		t.Error("the card copied the price, which a counter-offer would leave stale")
	}

	// The buyer opened it, so answering is the seller's move.
	if err := mustErr(h.svc.CounterOffer(ctx, orderapi.CounterOfferRequest{
		ActorID: buyer, ID: offer.ID, Quantity: 1, Total: 70_000,
	})); status(t, err) != 403 {
		t.Error("the buyer countered their own standing proposal")
	}
	countered, err := h.svc.CounterOffer(ctx, orderapi.CounterOfferRequest{
		ActorID: seller, ID: offer.ID, Quantity: 1, Total: 90_000, Reason: "firm",
	})
	if err != nil {
		t.Fatalf("CounterOffer: %v", err)
	}
	if countered.AuthorID != seller || countered.Total != 90_000 {
		t.Fatalf("offer = %+v, want the seller's terms", countered)
	}
	// The seller cannot turn their own price into a sale.
	if err := mustErr(h.svc.AcceptOffer(ctx, orderapi.AcceptOfferRequest{
		ActorID: seller, ID: offer.ID, ContactID: contactID, TransportOption: "ghn-express",
	})); status(t, err) != 403 {
		t.Error("the seller accepted their own offer")
	}

	result, err := h.svc.AcceptOffer(ctx, orderapi.AcceptOfferRequest{
		ActorID: buyer, ID: offer.ID, ContactID: contactID, TransportOption: "ghn-express",
	})
	if err != nil {
		t.Fatalf("AcceptOffer: %v", err)
	}
	if result.Total != 90_000 {
		t.Fatalf("checkout total = %d, want the agreed price", result.Total)
	}
	// And the money still creates the order — the seller is not asked again.
	if err := h.svc.SettlePaidSession(ctx, result.PaymentSession); err != nil {
		t.Fatalf("SettlePaidSession: %v", err)
	}
	page, err := h.svc.ListOrders(ctx, orderapi.ListOrdersRequest{ActorID: buyer, Role: "buyer", Limit: 10})
	if err != nil {
		t.Fatalf("ListOrders: %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].OfferID == nil || page.Data[0].DraftID != nil {
		t.Fatalf("order = %+v, want it to name the offer it came from", page.Data)
	}
}

// One negotiation per (buyer, variant): the terms are revised in place, so a second open one
// would be two answers to the same question.
func TestCreateOffer_OneActivePerVariant(t *testing.T) {
	h := newHarness("negotiable")
	ctx := context.Background()
	req := orderapi.CreateOfferRequest{ActorID: buyer, VariantID: variantID, Quantity: 1, Total: 80_000}
	if _, err := h.svc.CreateOffer(ctx, req); err != nil {
		t.Fatalf("CreateOffer: %v", err)
	}
	if got := status(t, mustErr(h.svc.CreateOffer(ctx, req))); got != 409 {
		t.Fatalf("status = %d, want 409", got)
	}
}

// Confirming receipt needs evidence and starts the payout clock; the payout then follows the
// window with no refund in the way.
func TestOrder_ReceiptThenPayout(t *testing.T) {
	h := newHarness("fixed")
	ctx := context.Background()
	_, o := h.checkout(t)

	// Evidence that names no confirmed upload is refused: a dispute judged on a photo that
	// does not render is a decision nobody can review.
	if got := status(t, mustErr(h.svc.ConfirmReceipt(ctx, orderapi.ConfirmReceiptRequest{
		ActorID: buyer, ID: o.ID, Attachments: []id.ID[id.Resource]{id.Of[id.Resource](42)},
	}))); got != 404 {
		t.Fatalf("status = %d, want 404 for unknown evidence", got)
	}
	h.repo.resources[42] = true
	confirmed, err := h.svc.ConfirmReceipt(ctx, orderapi.ConfirmReceiptRequest{
		ActorID: buyer, ID: o.ID, Attachments: []id.ID[id.Resource]{id.Of[id.Resource](42)},
	})
	if err != nil {
		t.Fatalf("ConfirmReceipt: %v", err)
	}
	if confirmed.ReceivedAt == nil || confirmed.PayoutDeadlineAt == nil {
		t.Fatalf("order = %+v, want the payout clock started", confirmed)
	}
	// The receipt is what starts the escrow window, so the run following the order is told.
	if !h.workflows.saw(fmt.Sprintf("order-received:%d", o.ID.Int64())) {
		t.Errorf("the escrow window was never started; calls = %v", h.workflows.calls)
	}

	// Not due yet: the window has not passed.
	if paid, err := h.svc.ReleaseDuePayouts(ctx, 10); err != nil || paid != 0 {
		t.Fatalf("ReleaseDuePayouts = %d, %v; want nothing due", paid, err)
	}
	// Wind the receipt back past the window, as the clock would.
	stored := h.repo.orders[o.ID.Int64()]
	stored.ReceivedAt = new(stored.ReceivedAt.Add(-domain.PayoutWindow - 1))
	h.repo.orders[o.ID.Int64()] = stored

	paid, err := h.svc.ReleaseDuePayouts(ctx, 10)
	if err != nil {
		t.Fatalf("ReleaseDuePayouts: %v", err)
	}
	if paid != 1 || h.finance.released != 100_000 {
		t.Fatalf("paid = %d released = %d, want the escrow out", paid, h.finance.released)
	}
	// And a second pass pays nothing: the order is completed, so it is no longer due.
	if paid, err := h.svc.ReleaseDuePayouts(ctx, 10); err != nil || paid != 0 {
		t.Fatalf("second pass = %d, %v; want nothing left", paid, err)
	}
}

// A refund holds the payout back, and the three windows advance in one pass.
func TestRefund_BlocksPayoutAndAdvancesOnTime(t *testing.T) {
	h := newHarness("fixed")
	ctx := context.Background()
	_, o := h.checkout(t)
	h.repo.resources[42] = true
	if _, err := h.svc.ConfirmReceipt(ctx, orderapi.ConfirmReceiptRequest{
		ActorID: buyer, ID: o.ID, Attachments: []id.ID[id.Resource]{id.Of[id.Resource](42)},
	}); err != nil {
		t.Fatalf("ConfirmReceipt: %v", err)
	}
	refund, err := h.svc.CreateRefund(ctx, orderapi.CreateRefundRequest{
		ActorID: buyer, OrderID: o.ID, Reason: "not as described",
	})
	if err != nil {
		t.Fatalf("CreateRefund: %v", err)
	}
	if refund.Status != domain.RefundAwaitingSeller || refund.DeadlineAt == nil {
		t.Fatalf("refund = %+v, want the seller on the clock", refund)
	}
	// The case has its own clock, and the escrow window is told to stop counting down.
	for _, want := range []string{
		fmt.Sprintf("start-refund:%d", refund.ID.Int64()),
		fmt.Sprintf("refund-raised:%d", o.ID.Int64()),
	} {
		if !h.workflows.saw(want) {
			t.Errorf("timer %q was never set; calls = %v", want, h.workflows.calls)
		}
	}
	// One live refund per order: a refund covers the whole order.
	if got := status(t, mustErr(h.svc.CreateRefund(ctx, orderapi.CreateRefundRequest{
		ActorID: buyer, OrderID: o.ID, Reason: "again",
	}))); got != 409 {
		t.Fatalf("status = %d, want 409", got)
	}

	// The escrow window has passed, but the live refund holds the payout back.
	stored := h.repo.orders[o.ID.Int64()]
	stored.ReceivedAt = new(stored.ReceivedAt.Add(-domain.PayoutWindow - 1))
	h.repo.orders[o.ID.Int64()] = stored
	if paid, err := h.svc.ReleaseDuePayouts(ctx, 10); err != nil || paid != 0 {
		t.Fatalf("ReleaseDuePayouts = %d, %v; want the refund to hold it", paid, err)
	}

	// The seller says nothing: the deadline passes and it lands on the buyer with no reason,
	// which is what tells a lapse from a refusal.
	live := h.repo.refunds[refund.ID.Int64()]
	live.DeadlineAt = new(live.CreatedAt.Add(-1))
	h.repo.refunds[refund.ID.Int64()] = live
	moved, err := h.svc.AdvanceOverdueRefunds(ctx, 10)
	if err != nil {
		t.Fatalf("AdvanceOverdueRefunds: %v", err)
	}
	if moved != 1 {
		t.Fatalf("advanced = %d, want the seller's window to lapse", moved)
	}
	after, err := h.svc.GetRefund(ctx, orderapi.RefundRequest{ActorID: buyer, ID: refund.ID})
	if err != nil {
		t.Fatalf("GetRefund: %v", err)
	}
	if after.Status != domain.RefundAwaitingBuyer || after.RejectionReason != nil {
		t.Fatalf("refund = %+v, want the buyer on the clock with no reason", after)
	}
}

// The seller granting a refund opens the return leg; the goods come back before the money
// goes, because a refund that is never granted ships nothing.
func TestRefund_AcceptOpensTheReturnLeg(t *testing.T) {
	h := newHarness("fixed")
	ctx := context.Background()
	_, o := h.checkout(t)
	h.repo.resources[42] = true
	if _, err := h.svc.ConfirmReceipt(ctx, orderapi.ConfirmReceiptRequest{
		ActorID: buyer, ID: o.ID, Attachments: []id.ID[id.Resource]{id.Of[id.Resource](42)},
	}); err != nil {
		t.Fatalf("ConfirmReceipt: %v", err)
	}
	refund, err := h.svc.CreateRefund(ctx, orderapi.CreateRefundRequest{
		ActorID: buyer, OrderID: o.ID, Reason: "damaged",
	})
	if err != nil {
		t.Fatalf("CreateRefund: %v", err)
	}
	// Only the seller decides, and a rejection needs a reason.
	if got := status(t, mustErr(h.svc.AcceptRefund(ctx, orderapi.RefundRequest{
		ActorID: buyer, ID: refund.ID,
	}))); got != 403 {
		t.Fatalf("status = %d, want 403 for the buyer accepting", got)
	}
	accepted, err := h.svc.AcceptRefund(ctx, orderapi.RefundRequest{ActorID: seller, ID: refund.ID})
	if err != nil {
		t.Fatalf("AcceptRefund: %v", err)
	}
	if accepted.Status != domain.RefundReturning || accepted.DeadlineAt != nil {
		t.Fatalf("refund = %+v, want it returning with nobody on the clock", accepted)
	}
	// The money has not moved: the goods come back first.
	if h.finance.refunded != 0 {
		t.Errorf("refunded = %d, want nothing paid before the return", h.finance.refunded)
	}
}

// A rejection puts the buyer on the clock, escalating puts a moderator in, and the verdict
// pays the buyer and closes the order.
func TestRefund_DisputeRuling(t *testing.T) {
	h := newHarness("fixed")
	ctx := context.Background()
	_, o := h.checkout(t)
	h.repo.resources[42] = true
	if _, err := h.svc.ConfirmReceipt(ctx, orderapi.ConfirmReceiptRequest{
		ActorID: buyer, ID: o.ID, Attachments: []id.ID[id.Resource]{id.Of[id.Resource](42)},
	}); err != nil {
		t.Fatalf("ConfirmReceipt: %v", err)
	}
	refund, err := h.svc.CreateRefund(ctx, orderapi.CreateRefundRequest{
		ActorID: buyer, OrderID: o.ID, Reason: "not as described",
	})
	if err != nil {
		t.Fatalf("CreateRefund: %v", err)
	}
	if _, err := h.svc.RejectRefund(ctx, orderapi.RejectRefundRequest{
		ActorID: seller, ID: refund.ID, Reason: "sent as described",
	}); err != nil {
		t.Fatalf("RejectRefund: %v", err)
	}
	dispute, err := h.svc.OpenDispute(ctx, orderapi.OpenDisputeRequest{
		ActorID: buyer, ID: refund.ID, Reason: "photos show otherwise",
	})
	if err != nil {
		t.Fatalf("OpenDispute: %v", err)
	}
	if dispute.Round != 1 {
		t.Fatalf("round = %d, want the buyer's first", dispute.Round)
	}

	// The queue is staff-only.
	if got := status(t, mustErr(h.svc.AdminListDisputes(ctx, orderapi.ListDisputesRequest{
		ActorID: buyer, Limit: 10,
	}))); got != 403 {
		t.Fatalf("status = %d, want 403", got)
	}
	mod := h.moderator()
	queue, err := mod.AdminListDisputes(ctx, orderapi.ListDisputesRequest{ActorID: admin, Limit: 10})
	if err != nil {
		t.Fatalf("AdminListDisputes: %v", err)
	}
	if len(queue.Data) != 1 {
		t.Fatalf("queue = %+v, want the open dispute", queue.Data)
	}
	ruled, err := mod.AdminRuleDispute(ctx, orderapi.RuleDisputeRequest{
		ActorID: admin, ID: dispute.ID, BuyerWins: true, Note: "evidence is clear",
	})
	if err != nil {
		t.Fatalf("AdminRuleDispute: %v", err)
	}
	if ruled.Status != domain.DisputeBuyerWins || ruled.RuledAt == nil {
		t.Fatalf("dispute = %+v, want a recorded verdict", ruled)
	}
	// A round is ruled once: a later round argues against what this one decided.
	if got := status(t, mustErr(mod.AdminRuleDispute(ctx, orderapi.RuleDisputeRequest{
		ActorID: admin, ID: dispute.ID, BuyerWins: false,
	}))); got != 409 {
		t.Fatalf("status = %d, want 409", got)
	}
}

// A line cancelled before the money lands gives its units back, and one an order covers
// cannot be cancelled at all — that is what a refund is for.
func TestCancelItem_ReleasesStockBeforePayment(t *testing.T) {
	h := newHarness("fixed")
	ctx := context.Background()
	draft, err := h.svc.CreateDraft(ctx, orderapi.CreateDraftRequest{ActorID: buyer, ListingID: listingID})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	result, err := h.svc.Checkout(ctx, orderapi.CheckoutRequest{
		ActorID: buyer, ID: draft.ID, ContactID: contactID, Currency: "VND",
		TransportOption: "ghn-express",
		Lines:           []orderapi.CheckoutLine{{VariantID: variantID, Quantity: 2}},
	})
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	if h.catalog.reserved != 2 {
		t.Fatalf("reserved = %d, want the checkout to hold them", h.catalog.reserved)
	}
	if _, err := h.svc.CancelItem(ctx, orderapi.ItemRequest{
		ActorID: buyer, ID: result.Items[0].ID,
	}); err != nil {
		t.Fatalf("CancelItem: %v", err)
	}
	if h.catalog.reserved != 0 {
		t.Errorf("reserved = %d, want the units back", h.catalog.reserved)
	}
	// With every line gone there is nothing left to pay for, so the run stops waiting rather
	// than holding its timer to the end.
	if !h.workflows.saw("checkout-cancelled:1") {
		t.Errorf("an abandoned checkout is still waiting on its timer; calls = %v", h.workflows.calls)
	}

	// Cancelling twice is refused rather than releasing twice.
	if got := status(t, mustErr(h.svc.CancelItem(ctx, orderapi.ItemRequest{
		ActorID: buyer, ID: result.Items[0].ID,
	}))); got != 409 {
		t.Fatalf("status = %d, want 409", got)
	}
}

// A seller with no collection point stops the sale rather than shipping from a guessed
// address — and the money is already taken, so this is the one place it must be loud.
func TestSettlePaidSession_NeedsAPickupAddress(t *testing.T) {
	h := newHarness("fixed")
	h.svc = order.NewService(h.repo, fakeAccounts{role: "user", noPickup: true}, h.catalog,
		h.finance, h.chat, h.repo, h.workflows, eventbus.NewMemory(slog.New(slog.DiscardHandler)),
		validation.Default(), slog.New(slog.DiscardHandler))
	ctx := context.Background()
	draft, err := h.svc.CreateDraft(ctx, orderapi.CreateDraftRequest{ActorID: buyer, ListingID: listingID})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	result, err := h.svc.Checkout(ctx, orderapi.CheckoutRequest{
		ActorID: buyer, ID: draft.ID, ContactID: contactID, Currency: "VND",
		TransportOption: "ghn-express",
		Lines:           []orderapi.CheckoutLine{{VariantID: variantID, Quantity: 1}},
	})
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	if err := h.svc.SettlePaidSession(ctx, result.PaymentSession); err == nil {
		t.Fatal("an order shipped from nowhere")
	}
}

// The expiry passes close what nobody finished: a draft holds a frozen price, a negotiation
// holds nothing, and both are idempotent so a retried timer is harmless.
func TestExpiry_ClosesWhatNobodyFinished(t *testing.T) {
	h := newHarness("negotiable")
	ctx := context.Background()
	offer, err := h.svc.CreateOffer(ctx, orderapi.CreateOfferRequest{
		ActorID: buyer, VariantID: variantID, Quantity: 1, Total: 80_000,
	})
	if err != nil {
		t.Fatalf("CreateOffer: %v", err)
	}
	stored := h.repo.offers[offer.ID.Int64()]
	stored.ExpiresAt = stored.CreatedAt.Add(-1)
	h.repo.offers[offer.ID.Int64()] = stored

	closed, err := h.svc.ExpireOffers(ctx, 10)
	if err != nil {
		t.Fatalf("ExpireOffers: %v", err)
	}
	if closed != 1 {
		t.Fatalf("closed = %d, want the stale negotiation", closed)
	}
	// Again closes nothing: the row already moved.
	if closed, err := h.svc.ExpireOffers(ctx, 10); err != nil || closed != 0 {
		t.Fatalf("second pass = %d, %v; want nothing left", closed, err)
	}
}
