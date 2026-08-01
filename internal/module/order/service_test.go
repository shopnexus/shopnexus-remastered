package order_test

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"testing"
	"time"

	"shopnexus/internal/infra/eventbus"
	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/api/accounttest"
	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/module/catalog/api/catalogtest"
	chatapi "shopnexus/internal/module/chat/api"
	"shopnexus/internal/module/chat/api/chattest"
	financeapi "shopnexus/internal/module/finance/api"
	"shopnexus/internal/module/finance/api/financetest"
	financedomain "shopnexus/internal/module/finance/domain"
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

// fakeCatalog answers what a listing costs and holds its stock. The counters are real and so are
// their guards: a fake that subtracted whatever it was handed made every wrong-counter bug in
// this module invisible, because `reserved` was allowed to go negative.
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
	// movements is the idempotency ledger the real commit writes beside the counter, so a
	// retried settlement is a no-op here too.
	movements map[string]bool
}

func (f *fakeCatalog) GetListing(_ context.Context, req catalogapi.GetListingRequest) (catalogapi.ListingDetail, error) {
	return f.listing(), nil
}

// ListListings answers the card, currency and all: an offer's total is rendered against it, and a
// fake that left it blank would make the contract's claim untestable.
func (f *fakeCatalog) ListListings(context.Context, catalogapi.ListListingsRequest) (catalogapi.ListingPage, error) {
	return catalogapi.ListingPage{Data: []catalogapi.Listing{{
		ID: listingID, Name: "Ao thun", Currency: "VND", PriceMode: f.priceMode,
		Seller: accountapi.AccountSummary{ID: seller},
	}}}, nil
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

// ReleaseStock refuses what the real `WHERE reserved >= @units` refuses. That guard is the whole
// point of the fake: releasing units that are in `sold` has to fail here, or using the wrong
// reversal reads as success.
func (f *fakeCatalog) ReleaseStock(_ context.Context, req catalogapi.StockMovementRequest) error {
	if f.reserved < req.Units {
		return errx.NewError(409, "insufficient_stock", "nothing reserved to release")
	}
	f.reserved -= req.Units
	return nil
}

func (f *fakeCatalog) CommitStock(_ context.Context, req catalogapi.StockCommitRequest) error {
	return f.move(req, func() error {
		if f.reserved < req.Units {
			return errx.NewError(409, "insufficient_stock", "nothing reserved to commit")
		}
		f.reserved -= req.Units
		f.sold += req.Units
		return nil
	})
}

func (f *fakeCatalog) UncommitStock(_ context.Context, req catalogapi.StockCommitRequest) error {
	return f.move(req, func() error {
		if f.sold < req.Units {
			return errx.NewError(409, "insufficient_stock", "nothing sold to reverse")
		}
		f.sold -= req.Units
		return nil
	})
}

// move applies a keyed movement once, as the real one does by writing the key in the same
// transaction as the counter.
func (f *fakeCatalog) move(req catalogapi.StockCommitRequest, apply func() error) error {
	if req.IdempotencyKey == "" {
		return errx.NewError(500, "stock_movement_key_required", "no key")
	}
	if f.movements[req.IdempotencyKey] {
		return nil
	}
	if err := apply(); err != nil {
		return err
	}
	f.movements[req.IdempotencyKey] = true
	return nil
}

// fakeFinance is the money. It records what was held, released and refunded, and — the part that
// matters — refuses to move out more than is being held: `wallet_held_balance_non_negative` is a
// real constraint, and without it releasing an escrow that has already gone back to the buyer
// just incremented a counter, so paying both parties looked like success.
type fakeFinance struct {
	financetest.Stub
	nextSession int64
	held        int64
	released    int64
	refunded    int64
	// sessions is what each opened checkout looks like now: `paid` is what stops a line that
	// has been charged for from being cancelled.
	sessions map[int64]*financeapi.Session
	// posted is the idempotency index: a key used twice is refused, as the real one does.
	posted map[string]bool
	// holdFails and refundFails make the money unreachable for one attempt, which is the only
	// way to see whether a half-finished write is recoverable.
	holdFails   bool
	refundFails bool
}

func newFakeFinance() *fakeFinance {
	return &fakeFinance{
		sessions: map[int64]*financeapi.Session{},
		posted:   map[string]bool{},
	}
}

func (f *fakeFinance) OpenCheckout(_ context.Context, req financeapi.OpenCheckoutRequest) (financeapi.Session, error) {
	f.nextSession++
	session := financeapi.Session{
		ID: id.Of[id.PaymentSession](f.nextSession), Kind: "buyer-checkout",
		Status: "pending", Currency: req.Currency, TotalAmount: req.Total,
		Outstanding: req.Total,
	}
	f.sessions[f.nextSession] = &session
	return session, nil
}

func (f *fakeFinance) GetSession(_ context.Context, req financeapi.GetSessionRequest) (financeapi.Session, error) {
	session, ok := f.sessions[req.ID.Int64()]
	if !ok {
		return financeapi.Session{}, errx.NewError(404, "session_not_found", "no such session")
	}
	return *session, nil
}

// pay marks a session covered, which is what the webhook does before order settles it.
func (f *fakeFinance) pay(sessionID id.ID[id.PaymentSession]) {
	if session, ok := f.sessions[sessionID.Int64()]; ok {
		session.Status = "success"
		session.PaidAt = new(time.Now())
	}
}

func (f *fakeFinance) HoldEscrow(_ context.Context, req financeapi.EscrowRequest) error {
	if f.holdFails {
		return errx.NewError(503, "finance_unreachable", "the ledger is down")
	}
	return f.post(req, func() error {
		f.held += req.Amount
		return nil
	})
}

func (f *fakeFinance) ReleaseEscrow(_ context.Context, req financeapi.EscrowRequest) error {
	return f.post(req, func() error {
		if err := f.debitHeld(req.Amount); err != nil {
			return err
		}
		f.released += req.Amount
		return nil
	})
}

func (f *fakeFinance) RefundEscrow(_ context.Context, req financeapi.EscrowRequest) error {
	if f.refundFails {
		return errx.NewError(503, "finance_unreachable", "the ledger is down")
	}
	return f.post(req, func() error {
		if err := f.debitHeld(req.Amount); err != nil {
			return err
		}
		f.refunded += req.Amount
		return nil
	})
}

// debitHeld is wallet_held_balance_non_negative: money can only leave escrow once, so a second
// party being paid out of the same hold is refused rather than counted.
func (f *fakeFinance) debitHeld(amount int64) error {
	if f.held < amount {
		return errx.NewError(409, "held_balance_negative", "escrow does not hold that much")
	}
	f.held -= amount
	return nil
}

func (f *fakeFinance) post(req financeapi.EscrowRequest, apply func() error) error {
	if f.posted[req.IdempotencyKey] {
		// The real sentinel, not a lookalike: order treats this one as success, and a coded
		// error that merely reads the same would make every resumed settlement fail.
		return financedomain.ErrMovementAlreadyPosted
	}
	if err := apply(); err != nil {
		return err
	}
	f.posted[req.IdempotencyKey] = true
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
	uploads   *fakeUploads
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

func (f *fakeWorkflows) OrderCancelled(_ context.Context, orderID int64) error {
	return f.record("order-cancelled", orderID)
}

func (f *fakeWorkflows) RefundRaised(_ context.Context, orderID int64) error {
	return f.record("refund-raised", orderID)
}

func (f *fakeWorkflows) RefundResolved(_ context.Context, orderID int64, buyerPaid bool) error {
	return f.record(fmt.Sprintf("refund-resolved(%t)", buyerPaid), orderID)
}

func (f *fakeWorkflows) StartRefundWindow(_ context.Context, refundID int64, status string) error {
	return f.record("refund-window:"+status, refundID)
}

func newHarness(priceMode string) *harness {
	repo := newFakeRepo()
	catalog := &fakeCatalog{
		priceMode: priceMode, price: 100_000, available: 5,
		movements: map[string]bool{},
	}
	finance := newFakeFinance()
	chat := &fakeChat{}
	uploads := newFakeUploads()
	accounts := fakeAccounts{role: "user"}
	workflows := &fakeWorkflows{}
	svc := order.NewService(repo, accounts, catalog, finance, chat, uploads, repo, workflows,
		eventbus.NewMemory(slog.New(slog.DiscardHandler)), validation.Default(),
		slog.New(slog.DiscardHandler))
	return &harness{svc: svc, repo: repo, catalog: catalog, finance: finance, chat: chat,
		uploads: uploads, accounts: accounts, workflows: workflows}
}

// ageItems winds every line back, which is how a test reaches a checkout window the clock would
// otherwise have to wait out.
func (h *harness) ageItems(by time.Duration) {
	for key, i := range h.repo.items {
		i.CreatedAt = i.CreatedAt.Add(by)
		h.repo.items[key] = i
	}
}

// moderator reuses one harness's repository with a staff caller.
func (h *harness) moderator() *order.Service {
	return order.NewService(h.repo, fakeAccounts{role: "moderator"}, h.catalog, h.finance,
		h.chat, h.uploads, h.repo, h.workflows, eventbus.NewMemory(slog.New(slog.DiscardHandler)),
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
	h.finance.pay(result.PaymentSession)
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
	// A total is not a price without the currency beside it, and the offer row carries none —
	// the listing decides it, so a read resolves it rather than every revision copying it.
	read, err := h.svc.GetOffer(ctx, orderapi.OfferRequest{ActorID: buyer, ID: offer.ID})
	if err != nil {
		t.Fatalf("GetOffer: %v", err)
	}
	if read.Currency != "VND" {
		t.Errorf("currency = %q, want the listing's", read.Currency)
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
	h.finance.pay(result.PaymentSession)
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
	h.uploads.confirm(42)
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

// The evidence a receipt confirmation carries has to come from this module's own upload seam:
// reserve a slot, PUT lands the bytes, confirm makes the row real, and only then can it be
// named — because a dispute is decided on this evidence, and today nothing else can create a
// confirmed upload for ConfirmReceipt to accept.
func TestUpload_ConfirmedBeforeItCanBeUsedAsEvidence(t *testing.T) {
	h := newHarness("fixed")
	ctx := context.Background()
	_, o := h.checkout(t)

	slot, err := h.svc.CreateUpload(ctx, orderapi.CreateUploadRequest{
		ActorID: buyer, Filename: "unbox.jpg", Mime: "image/jpeg", Size: 2048,
	})
	if err != nil {
		t.Fatalf("CreateUpload: %v", err)
	}
	if slot.URL == "" || slot.ResourceID == 0 || !slot.ExpiresAt.After(time.Now()) {
		t.Fatalf("slot = %+v, want somewhere to PUT and a future expiry", slot)
	}

	// Unconfirmed, so it names no usable upload: attaching it is refused exactly as a made-up
	// id would be.
	if got := status(t, mustErr(h.svc.ConfirmReceipt(ctx, orderapi.ConfirmReceiptRequest{
		ActorID: buyer, ID: o.ID, Attachments: []id.ID[id.Resource]{slot.ResourceID},
	}))); got != 404 {
		t.Fatalf("status = %d, want 404 attaching an unconfirmed upload", got)
	}
	// And confirming before the bytes are there is refused too, rather than producing a row
	// that renders as a broken image.
	if err := mustErr(h.svc.ConfirmUpload(ctx, orderapi.ConfirmUploadRequest{
		ActorID: buyer, ID: slot.ResourceID,
	})); err == nil {
		t.Fatal("an upload was confirmed before anything was uploaded")
	}

	// The client PUTs, then confirms.
	h.uploads.arrived[slot.ResourceID.Int64()] = true
	confirmedUpload, err := h.svc.ConfirmUpload(ctx, orderapi.ConfirmUploadRequest{
		ActorID: buyer, ID: slot.ResourceID,
	})
	if err != nil {
		t.Fatalf("ConfirmUpload: %v", err)
	}
	if confirmedUpload.ID != slot.ResourceID {
		t.Fatalf("confirmed = %+v, want the slot's own resource", confirmedUpload)
	}

	// Now it attaches, and the order renders it with a live link rather than a bare id.
	received, err := h.svc.ConfirmReceipt(ctx, orderapi.ConfirmReceiptRequest{
		ActorID: buyer, ID: o.ID, Attachments: []id.ID[id.Resource]{slot.ResourceID},
	})
	if err != nil {
		t.Fatalf("ConfirmReceipt: %v", err)
	}
	if len(received.ReceiptAttachments) != 1 || received.ReceiptAttachments[0].URL == "" {
		t.Fatalf("attachments = %+v, want one with a signed link on it", received.ReceiptAttachments)
	}

	// Somebody else's slot is not theirs to confirm: a resource id is guessable.
	other, err := h.svc.CreateUpload(ctx, orderapi.CreateUploadRequest{
		ActorID: buyer, Filename: "back.jpg", Mime: "image/jpeg", Size: 1024,
	})
	if err != nil {
		t.Fatalf("CreateUpload: %v", err)
	}
	h.uploads.arrived[other.ResourceID.Int64()] = true
	if err := mustErr(h.svc.ConfirmUpload(ctx, orderapi.ConfirmUploadRequest{
		ActorID: seller, ID: other.ResourceID,
	})); err == nil {
		t.Fatal("a stranger confirmed somebody else's upload slot")
	}
}

// A refund holds the payout back, and the three windows advance in one pass.
func TestRefund_BlocksPayoutAndAdvancesOnTime(t *testing.T) {
	h := newHarness("fixed")
	ctx := context.Background()
	_, o := h.checkout(t)
	h.uploads.confirm(42)
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
		fmt.Sprintf("refund-window:%s:%d", domain.RefundAwaitingSeller, refund.ID.Int64()),
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
	h.uploads.confirm(42)
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

// A rejection puts the buyer on the clock, escalating puts a moderator in, and a verdict for the
// buyer in round one books the return leg — the state has no other exit, so a ruling that only
// set the status stranded the escrow with nobody on a clock. The money then follows the goods
// back: the return is delivered, the seller does not appeal, and the window lapsing pays the
// buyer and closes the order.
func TestRefund_DisputeRuling(t *testing.T) {
	h := newHarness("fixed")
	ctx := context.Background()
	_, o := h.checkout(t)
	h.uploads.confirm(42)
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

	// The verdict moved the case to `returning` *and* booked the leg, which is the only way out
	// of that state. Nothing has been paid yet: the goods come back first.
	granted := h.repo.refunds[refund.ID.Int64()]
	if granted.Status != domain.RefundReturning || granted.ReturnTransportID == nil {
		t.Fatalf("refund = %+v, want it returning with a leg to track", granted)
	}
	if h.finance.refunded != 0 {
		t.Errorf("refunded = %d, want nothing paid before the return", h.finance.refunded)
	}

	// The parcel arrives. That is what opens the seller's inspection window — and it has a
	// writer now, so the case is no longer stuck.
	returned, err := h.svc.AdvanceReturnShipment(ctx, orderapi.AdvanceReturnShipmentRequest{
		ActorID: buyer, ID: refund.ID, Status: domain.TransportDelivered,
	})
	if err != nil {
		t.Fatalf("AdvanceReturnShipment: %v", err)
	}
	if returned.Status != domain.RefundReturned || returned.DeadlineAt == nil {
		t.Fatalf("refund = %+v, want the seller's appeal window open", returned)
	}

	// The seller says nothing, so the window lapsing pays the buyer and closes the order.
	live := h.repo.refunds[refund.ID.Int64()]
	live.DeadlineAt = new(live.CreatedAt.Add(-1))
	h.repo.refunds[refund.ID.Int64()] = live
	if moved, err := h.svc.AdvanceOverdueRefunds(ctx, 10); err != nil || moved != 1 {
		t.Fatalf("AdvanceOverdueRefunds = %d, %v; want the appeal window to lapse", moved, err)
	}
	settled := h.repo.refunds[refund.ID.Int64()]
	if settled.Status != domain.RefundAccepted {
		t.Fatalf("refund = %+v, want it accepted", settled)
	}
	if h.finance.refunded != 100_000 || h.finance.held != 0 {
		t.Fatalf("refunded = %d held = %d, want the whole escrow back with the buyer",
			h.finance.refunded, h.finance.held)
	}
	closed := h.repo.orders[o.ID.Int64()]
	if closed.State() != domain.StateCancelled {
		t.Fatalf("order = %q, want it closed with the refund", closed.State())
	}
	// The units are back on the shelf, off `sold` rather than out of somebody else's
	// reservation — which is what releasing instead of uncommitting would have done.
	if h.catalog.sold != 0 || h.catalog.reserved != 0 {
		t.Fatalf("stock = reserved %d sold %d, want the sale reversed",
			h.catalog.reserved, h.catalog.sold)
	}
	// And nothing pays the seller afterwards: an accepted refund keeps its claim on the escrow.
	if paid, err := h.svc.ReleaseDuePayouts(ctx, 10); err != nil || paid != 0 {
		t.Fatalf("ReleaseDuePayouts = %d, %v; want the refunded order to be nobody's payout",
			paid, err)
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
		h.finance, h.chat, h.uploads, h.repo, h.workflows, eventbus.NewMemory(slog.New(slog.DiscardHandler)),
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

// startCheckout takes a fixed-price listing as far as an open payment session, which is where
// the tests about the unpaid half of the flow start.
func (h *harness) startCheckout(t *testing.T, quantity int64) orderapi.CheckoutResult {
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
		Lines:           []orderapi.CheckoutLine{{VariantID: variantID, Quantity: quantity}},
	})
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	return result
}

// Settling is resumable, not merely idempotent. The escrow hold fails once, so the order exists
// with no money behind it and no stock committed; the retry has to finish the job. It used to
// return nil for ever, because every pass re-applied the "lines with no order" filter and found
// none — a buyer who had paid, an order nobody held escrow for, and a payout 72h later out of
// somebody else's balance.
func TestSettlePaidSession_ResumesAfterAFailedHold(t *testing.T) {
	h := newHarness("fixed")
	ctx := context.Background()
	result := h.startCheckout(t, 1)
	h.finance.pay(result.PaymentSession)

	// The hold fails. The order is written by then, which is the whole difficulty.
	h.finance.holdFails = true
	if err := h.svc.SettlePaidSession(ctx, result.PaymentSession); err == nil {
		t.Fatal("a failed escrow hold reported success")
	}
	if h.finance.held != 0 || h.catalog.sold != 0 {
		t.Fatalf("held = %d sold = %d, want nothing past the order to have happened",
			h.finance.held, h.catalog.sold)
	}

	h.finance.holdFails = false
	if err := h.svc.SettlePaidSession(ctx, result.PaymentSession); err != nil {
		t.Fatalf("resumed SettlePaidSession: %v", err)
	}
	if h.finance.held != 100_000 {
		t.Fatalf("held = %d, want the retry to hold the escrow", h.finance.held)
	}
	if h.catalog.sold != 1 || h.catalog.reserved != 0 {
		t.Fatalf("stock = reserved %d sold %d, want the retry to commit the sale",
			h.catalog.reserved, h.catalog.sold)
	}
	// Once more changes nothing: the hold is keyed on the order, the sale on the line.
	if err := h.svc.SettlePaidSession(ctx, result.PaymentSession); err != nil {
		t.Fatalf("third SettlePaidSession: %v", err)
	}
	if h.finance.held != 100_000 || h.catalog.sold != 1 {
		t.Fatalf("held = %d sold = %d, want each effect applied once",
			h.finance.held, h.catalog.sold)
	}
	// One order, and every line of the session is on it.
	if len(h.repo.orders) != 1 {
		t.Fatalf("orders = %d, want the retries to mint none", len(h.repo.orders))
	}
	for _, i := range h.repo.items {
		if i.OrderID == nil {
			t.Fatalf("item %d is still unlinked after a resumed settlement", i.ID)
		}
	}
}

// A paid line is not the seller's to cancel. Not even a race is needed: when settling fails
// because the seller has no pickup address the paid lines sit on the retry list, and cancelling
// one there released the stock and made every later settlement a no-op — the buyer's money
// captured against nothing.
func TestCancelItem_RefusesAPaidLine(t *testing.T) {
	h := newHarness("fixed")
	ctx := context.Background()
	result := h.startCheckout(t, 1)
	h.finance.pay(result.PaymentSession)

	if got := status(t, mustErr(h.svc.CancelItem(ctx, orderapi.ItemRequest{
		ActorID: seller, ID: result.Items[0].ID,
	}))); got != 409 {
		t.Fatalf("status = %d, want 409 cancelling a line the money reached", got)
	}
	// The buyer cannot either: from here the sale is undone by a refund, which the seller sees.
	if got := status(t, mustErr(h.svc.CancelItem(ctx, orderapi.ItemRequest{
		ActorID: buyer, ID: result.Items[0].ID,
	}))); got != 409 {
		t.Fatalf("status = %d, want 409 for the buyer too", got)
	}
	if h.catalog.reserved != 1 {
		t.Fatalf("reserved = %d, want the units still held for the sale", h.catalog.reserved)
	}
	// And settling still works, because nothing was cancelled out from under it.
	if err := h.svc.SettlePaidSession(ctx, result.PaymentSession); err != nil {
		t.Fatalf("SettlePaidSession: %v", err)
	}
	if h.finance.held != 100_000 || h.catalog.sold != 1 {
		t.Fatalf("held = %d sold = %d, want the sale to have gone through",
			h.finance.held, h.catalog.sold)
	}
}

// The draft is claimed before the money is asked for, so two checkouts of one purchase session
// cannot both open a payment session for the same frozen price. The loser is refused rather than
// noted in a log line nobody reads.
func TestCheckout_ClaimsTheDraftBeforeCharging(t *testing.T) {
	h := newHarness("fixed")
	ctx := context.Background()
	draft, err := h.svc.CreateDraft(ctx, orderapi.CreateDraftRequest{
		ActorID: buyer, ListingID: listingID,
	})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	req := orderapi.CheckoutRequest{
		ActorID: buyer, ID: draft.ID, ContactID: contactID, Currency: "VND",
		TransportOption: "ghn-express",
		Lines:           []orderapi.CheckoutLine{{VariantID: variantID, Quantity: 1}},
	}
	if _, err := h.svc.Checkout(ctx, req); err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	if got := status(t, mustErr(h.svc.Checkout(ctx, req))); got != 409 {
		t.Fatalf("status = %d, want 409 for the second checkout of one draft", got)
	}
	// One session, and the loser reserved nothing.
	if h.finance.nextSession != 1 {
		t.Fatalf("sessions = %d, want one charge for one frozen price", h.finance.nextSession)
	}
	if h.catalog.reserved != 1 {
		t.Fatalf("reserved = %d, want only the winner's units held", h.catalog.reserved)
	}
}

// A checkout nobody pays gives its stock back on the sweep, not only on a durable timer. With
// WORKFLOW_RUNTIME=off the timer does not exist, and nothing else in the schema ever looks at a
// reservation again — the units were lost for good.
func TestExpireCheckouts_ReleasesUnpaidReservations(t *testing.T) {
	h := newHarness("fixed")
	ctx := context.Background()
	result := h.startCheckout(t, 2)
	if h.catalog.reserved != 2 {
		t.Fatalf("reserved = %d, want the checkout to hold them", h.catalog.reserved)
	}
	// Not yet: the buyer is still inside the window.
	if closed, err := h.svc.ExpireCheckouts(ctx, 10); err != nil || closed != 0 {
		t.Fatalf("ExpireCheckouts = %d, %v; want nothing expired yet", closed, err)
	}
	h.ageItems(-time.Hour)

	closed, err := h.svc.ExpireCheckouts(ctx, 10)
	if err != nil {
		t.Fatalf("ExpireCheckouts: %v", err)
	}
	if closed != 1 || h.catalog.reserved != 0 {
		t.Fatalf("closed = %d reserved = %d, want the units back", closed, h.catalog.reserved)
	}
	// Again finds nothing, and a line the money did reach is never in the list at all.
	if closed, err := h.svc.ExpireCheckouts(ctx, 10); err != nil || closed != 0 {
		t.Fatalf("second pass = %d, %v; want nothing left", closed, err)
	}

	h2 := newHarness("fixed")
	paid := h2.startCheckout(t, 1)
	h2.finance.pay(paid.PaymentSession)
	h2.ageItems(-time.Hour)
	if closed, err := h2.svc.ExpireCheckouts(ctx, 10); err != nil || closed != 0 {
		t.Fatalf("ExpireCheckouts = %d, %v; want a paid line left alone", closed, err)
	}
	if h2.catalog.reserved != 1 {
		t.Fatalf("reserved = %d, want a paid checkout's units still held", h2.catalog.reserved)
	}
	_ = result
}

// The payout re-reads the refund guard when it writes, not only when it selects. Interleaved, the
// old code released the escrow to the seller and the verdict then refunded the buyer out of a
// hold that was gone — which the ledger answers for by consuming another order's balance.
func TestReleasePayout_LosesToARefundCommittedAfterTheSelect(t *testing.T) {
	h := newHarness("fixed")
	ctx := context.Background()
	_, o := h.checkout(t)
	h.uploads.confirm(42)
	if _, err := h.svc.ConfirmReceipt(ctx, orderapi.ConfirmReceiptRequest{
		ActorID: buyer, ID: o.ID, Attachments: []id.ID[id.Resource]{id.Of[id.Resource](42)},
	}); err != nil {
		t.Fatalf("ConfirmReceipt: %v", err)
	}
	stored := h.repo.orders[o.ID.Int64()]
	stored.ReceivedAt = new(stored.ReceivedAt.Add(-domain.PayoutWindow - 1))
	h.repo.orders[o.ID.Int64()] = stored

	// The sweep's candidate list, read before the refund exists.
	due, err := h.repo.PayoutDue(ctx, time.Now(), 10)
	if err != nil {
		t.Fatalf("PayoutDue: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("due = %+v, want the order the window has passed on", due)
	}
	// The buyer commits a refund in the gap the old code read across.
	if _, err := h.svc.CreateRefund(ctx, orderapi.CreateRefundRequest{
		ActorID: buyer, OrderID: o.ID, Reason: "not as described",
	}); err != nil {
		t.Fatalf("CreateRefund: %v", err)
	}
	// The claim re-asks the question under the order's lock, so this one is no longer the
	// payout's to take.
	claimed := due[0]
	if err := h.repo.ClaimPayout(ctx, &claimed); err == nil {
		t.Fatal("the payout claimed an order a refund had already taken")
	}
	if err := h.svc.ReleasePayout(ctx, o.ID); err != nil {
		t.Fatalf("ReleasePayout: %v", err)
	}
	if h.finance.released != 0 || h.finance.held != 100_000 {
		t.Fatalf("released = %d held = %d, want the escrow untouched",
			h.finance.released, h.finance.held)
	}
	// And a refund cannot be opened the other way round either: once the order is claimed the
	// escrow it was about has gone.
	h2 := newHarness("fixed")
	_, o2 := h2.checkout(t)
	h2.uploads.confirm(42)
	if _, err := h2.svc.ConfirmReceipt(ctx, orderapi.ConfirmReceiptRequest{
		ActorID: buyer, ID: o2.ID, Attachments: []id.ID[id.Resource]{id.Of[id.Resource](42)},
	}); err != nil {
		t.Fatalf("ConfirmReceipt: %v", err)
	}
	paid := h2.repo.orders[o2.ID.Int64()]
	paid.ReceivedAt = new(paid.ReceivedAt.Add(-domain.PayoutWindow - 1))
	h2.repo.orders[o2.ID.Int64()] = paid
	if n, err := h2.svc.ReleaseDuePayouts(ctx, 10); err != nil || n != 1 {
		t.Fatalf("ReleaseDuePayouts = %d, %v; want the payout to go through", n, err)
	}
	if got := status(t, mustErr(h2.svc.CreateRefund(ctx, orderapi.CreateRefundRequest{
		ActorID: buyer, OrderID: o2.ID, Reason: "too late",
	}))); got != 409 {
		t.Fatalf("status = %d, want 409 refunding an order already paid out", got)
	}
}

// A settled refund is one the money has already reached: the transfer comes first, and the row
// going terminal is what records that it did. Written the other way round, a transfer that failed
// left `accepted` on the row, nothing retried it, and the payout sweep handed the seller the
// money the buyer had been awarded.
func TestSettleRefund_MovesTheMoneyBeforeTheRowGoesTerminal(t *testing.T) {
	h := newHarness("fixed")
	ctx := context.Background()
	_, o := h.checkout(t)
	h.uploads.confirm(42)
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
	if _, err := h.svc.AcceptRefund(ctx, orderapi.RefundRequest{
		ActorID: seller, ID: refund.ID,
	}); err != nil {
		t.Fatalf("AcceptRefund: %v", err)
	}
	if _, err := h.svc.AdvanceReturnShipment(ctx, orderapi.AdvanceReturnShipmentRequest{
		ActorID: seller, ID: refund.ID, Status: domain.TransportDelivered,
	}); err != nil {
		t.Fatalf("AdvanceReturnShipment: %v", err)
	}

	// The transfer fails on the first attempt. The refund must not be `accepted` afterwards, or
	// nothing will ever try again.
	overdue := h.repo.refunds[refund.ID.Int64()]
	overdue.DeadlineAt = new(overdue.CreatedAt.Add(-1))
	h.repo.refunds[refund.ID.Int64()] = overdue
	h.finance.refundFails = true
	if _, err := h.svc.AdvanceOverdueRefunds(ctx, 10); err == nil {
		t.Fatal("a failed refund transfer reported success")
	}
	stalled := h.repo.refunds[refund.ID.Int64()]
	if stalled.Settled() {
		t.Fatalf("refund = %q, want a case the money never reached to stay live", stalled.Status)
	}
	// Still live, so it still blocks the payout — the seller is not paid in the meantime.
	if paid, err := h.svc.ReleaseDuePayouts(ctx, 10); err != nil || paid != 0 {
		t.Fatalf("ReleaseDuePayouts = %d, %v; want the unpaid refund to hold it", paid, err)
	}

	// And the sweep is what retries it: the window is still overdue.
	h.finance.refundFails = false
	if moved, err := h.svc.AdvanceOverdueRefunds(ctx, 10); err != nil || moved != 1 {
		t.Fatalf("AdvanceOverdueRefunds = %d, %v; want the sweep to finish it", moved, err)
	}
	settled := h.repo.refunds[refund.ID.Int64()]
	if settled.Status != domain.RefundAccepted {
		t.Fatalf("refund = %q, want it accepted once the money moved", settled.Status)
	}
	if h.finance.refunded != 100_000 || h.finance.held != 0 {
		t.Fatalf("refunded = %d held = %d, want the escrow back with the buyer once",
			h.finance.refunded, h.finance.held)
	}
	if h.repo.orders[o.ID.Int64()].State() != domain.StateCancelled {
		t.Fatal("the order stayed open under a settled refund")
	}
}

// A cancelled order puts its units back with the reversal of a sale, not with the reversal of a
// reservation: by then they are in `sold`, and decrementing `reserved` either fails or eats
// another buyer's hold and oversells.
func TestCancelOrder_ReversesTheSaleRatherThanAReservation(t *testing.T) {
	h := newHarness("fixed")
	ctx := context.Background()
	_, o := h.checkout(t)
	if h.catalog.sold != 1 || h.catalog.reserved != 0 {
		t.Fatalf("stock = reserved %d sold %d, want the sale committed",
			h.catalog.reserved, h.catalog.sold)
	}
	// Somebody else is mid-checkout on the same variant. Releasing instead of uncommitting would
	// take their reservation.
	if err := h.catalog.ReserveStock(ctx, catalogapi.StockMovementRequest{
		VariantID: variantID, Units: 1,
	}); err != nil {
		t.Fatalf("ReserveStock: %v", err)
	}

	cancelled, err := h.svc.CancelOrder(ctx, orderapi.CancelOrderRequest{ActorID: buyer, ID: o.ID})
	if err != nil {
		t.Fatalf("CancelOrder: %v", err)
	}
	if cancelled.State != domain.StateCancelled {
		t.Fatalf("state = %q, want cancelled", cancelled.State)
	}
	if h.catalog.sold != 0 {
		t.Fatalf("sold = %d, want the sale reversed", h.catalog.sold)
	}
	if h.catalog.reserved != 1 {
		t.Fatalf("reserved = %d, want the other buyer's hold untouched", h.catalog.reserved)
	}
	if h.finance.refunded != 100_000 || h.finance.held != 0 {
		t.Fatalf("refunded = %d held = %d, want the escrow back",
			h.finance.refunded, h.finance.held)
	}
	// The run is parked on the receipt, so that is the wait a cancellation has to end.
	if !h.workflows.saw(fmt.Sprintf("order-cancelled:%d", o.ID.Int64())) {
		t.Errorf("a cancelled order left its run waiting; calls = %v", h.workflows.calls)
	}
}

// A delivered order cannot be cancelled. The guard reads the shipment's status, and until
// something wrote it a buyer could take the whole escrow back and keep the goods.
func TestCancelOrder_RefusesAShippedOrder(t *testing.T) {
	h := newHarness("fixed")
	ctx := context.Background()
	_, o := h.checkout(t)

	// Only the seller reports the outbound leg; a buyer marking their own parcel shipped would
	// be deciding whether they may still cancel.
	if got := status(t, mustErr(h.svc.AdvanceShipment(ctx, orderapi.AdvanceShipmentRequest{
		ActorID: buyer, ID: o.ID, Status: domain.TransportPickedUp,
	}))); got != 403 {
		t.Fatalf("status = %d, want 403 for the buyer", got)
	}
	if _, err := h.svc.AdvanceShipment(ctx, orderapi.AdvanceShipmentRequest{
		ActorID: seller, ID: o.ID, Status: domain.TransportPickedUp,
	}); err != nil {
		t.Fatalf("AdvanceShipment: %v", err)
	}
	if got := status(t, mustErr(h.svc.CancelOrder(ctx, orderapi.CancelOrderRequest{
		ActorID: buyer, ID: o.ID,
	}))); got != 409 {
		t.Fatalf("status = %d, want 409 cancelling a parcel that has left", got)
	}
	if h.finance.refunded != 0 || h.finance.held != 100_000 {
		t.Fatalf("refunded = %d held = %d, want the escrow where it was",
			h.finance.refunded, h.finance.held)
	}
	// Forward-only: a late report cannot un-ship it.
	if got := status(t, mustErr(h.svc.AdvanceShipment(ctx, orderapi.AdvanceShipmentRequest{
		ActorID: seller, ID: o.ID, Status: domain.TransportPickedUp,
	}))); got != 409 {
		t.Fatalf("status = %d, want 409 for a checkpoint already passed", got)
	}
}

// Withdrawing is its own outcome. Stored as a rejection it was indistinguishable from a seller
// who won the case, and the schema has had a label for it all along.
func TestWithdrawRefund_IsNotASellerWin(t *testing.T) {
	h := newHarness("fixed")
	ctx := context.Background()
	_, o := h.checkout(t)
	h.uploads.confirm(42)
	if _, err := h.svc.ConfirmReceipt(ctx, orderapi.ConfirmReceiptRequest{
		ActorID: buyer, ID: o.ID, Attachments: []id.ID[id.Resource]{id.Of[id.Resource](42)},
	}); err != nil {
		t.Fatalf("ConfirmReceipt: %v", err)
	}
	refund, err := h.svc.CreateRefund(ctx, orderapi.CreateRefundRequest{
		ActorID: buyer, OrderID: o.ID, Reason: "changed my mind",
	})
	if err != nil {
		t.Fatalf("CreateRefund: %v", err)
	}
	if err := h.svc.WithdrawRefund(ctx, orderapi.RefundRequest{
		ActorID: buyer, ID: refund.ID,
	}); err != nil {
		t.Fatalf("WithdrawRefund: %v", err)
	}
	withdrawn := h.repo.refunds[refund.ID.Int64()]
	if withdrawn.Status != domain.RefundCancelled || withdrawn.DeadlineAt != nil {
		t.Fatalf("refund = %+v, want it cancelled with nobody on a clock", withdrawn)
	}
	// A withdrawal gives the escrow up, so the payout is the seller's again.
	stored := h.repo.orders[o.ID.Int64()]
	stored.ReceivedAt = new(stored.ReceivedAt.Add(-domain.PayoutWindow - 1))
	h.repo.orders[o.ID.Int64()] = stored
	if paid, err := h.svc.ReleaseDuePayouts(ctx, 10); err != nil || paid != 1 {
		t.Fatalf("ReleaseDuePayouts = %d, %v; want the seller paid", paid, err)
	}
	if h.finance.released != 100_000 {
		t.Fatalf("released = %d, want the escrow out", h.finance.released)
	}
}

// A page boundary inside a group of rows sharing one transaction's timestamp used to make the
// rest of that group unreachable: the cursor carried `created_at` alone while the ordering added
// `id`. Three lines of one checkout are exactly that group.
func TestListItems_PagesThroughRowsSharingATimestamp(t *testing.T) {
	h := newHarness("fixed")
	ctx := context.Background()
	at := time.Now()
	for i := range 3 {
		h.repo.items[int64(i+1)] = domain.Item{
			ID: int64(i + 1), BuyerID: buyer.Int64(), SellerID: seller.Int64(),
			ListingID: listingID.Int64(), VariantID: variantID.Int64(), Currency: "VND",
			Quantity: 1, TransportOption: "ghn-express", TotalAmount: 100_000,
			PaymentSessionID: 1, CreatedAt: at,
		}
	}
	seen := map[int64]bool{}
	cursor := ""
	for range 5 {
		page, err := h.svc.ListItems(ctx, orderapi.ListItemsRequest{
			ActorID: buyer, Role: "buyer", Limit: 2, Cursor: cursor,
		})
		if err != nil {
			t.Fatalf("ListItems: %v", err)
		}
		for _, i := range page.Data {
			seen[i.ID.Int64()] = true
		}
		if !page.Meta.HasMore {
			break
		}
		cursor = page.Meta.NextCursor
	}
	if len(seen) != 3 {
		t.Fatalf("saw %d of 3 lines, want every row reachable: %v", len(seen), seen)
	}
}
