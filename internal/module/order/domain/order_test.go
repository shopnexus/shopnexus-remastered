package domain_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"shopnexus/internal/module/order/domain"
)

func address() domain.AddressSnapshot {
	return domain.AddressSnapshot{FullName: "Nguyen Van A", Phone: "+84900000001", Country: "VN"}
}

func newOrder(t *testing.T) domain.Order {
	t.Helper()
	o, err := domain.NewOrder(domain.FromDraft(1), 7, 8, 9, address(), address())
	if err != nil {
		t.Fatalf("NewOrder: %v", err)
	}
	return o
}

// An order comes from a checkout or from an accepted offer, never both and never neither —
// which is the CHECK the schema also holds.
func TestNewOrder_NeedsExactlyOneOrigin(t *testing.T) {
	if _, err := domain.NewOrder(domain.Origin{}, 7, 8, 9, address(), address()); err == nil {
		t.Error("an order with no origin was accepted")
	}
	both := domain.Origin{DraftID: new(int64(1)), OfferID: new(int64(2))}
	if _, err := domain.NewOrder(both, 7, 8, 9, address(), address()); err == nil {
		t.Error("an order from both a draft and an offer was accepted")
	}
	if _, err := domain.NewOrder(domain.FromOffer(2), 7, 8, 9, address(), address()); err != nil {
		t.Errorf("a negotiated order was refused: %v", err)
	}
}

// The state is read from the timestamps rather than stored, so there is no separate fact to
// keep in step with them.
func TestOrder_StateIsDerived(t *testing.T) {
	o := newOrder(t)
	// A paid order starts out waiting on the seller, and nothing has been handed to a carrier.
	// Settled is read off the *outcome* timestamps, not off "not open" — this earliest state is
	// the one that made that distinction matter.
	if o.State() != domain.StateAwaitingConfirmation || o.Settled() {
		t.Fatalf("state = %q, want awaiting-confirmation", o.State())
	}
	if err := o.Confirm(); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if o.State() != domain.StateOpen || o.Settled() {
		t.Fatalf("state = %q, want open once the seller accepted", o.State())
	}
	// A second confirmation would book a second parcel for one sale.
	if err := o.Confirm(); !errors.Is(err, domain.ErrOrderAlreadyConfirmed) {
		t.Fatalf("second Confirm = %v, want ErrOrderAlreadyConfirmed", err)
	}
	// A confirmed receipt starts the payout clock; it does not end the order.
	if err := o.ConfirmReceipt([]int64{42}); err != nil {
		t.Fatalf("ConfirmReceipt: %v", err)
	}
	if o.State() != domain.StateOpen {
		t.Fatalf("state = %q, want it still open after a receipt", o.State())
	}
	// Completion is the payout's own write — the conditional UPDATE that decides the escrow
	// race — so the state is read back from the timestamp it sets.
	o.CompletedAt = new(time.Now())
	if o.State() != domain.StateCompleted || !o.Settled() {
		t.Fatalf("state = %q, want completed", o.State())
	}
	// Nothing moves a settled order: the payout already happened.
	if err := o.Cancel(false); !errors.Is(err, domain.ErrOrderSettled) {
		t.Fatalf("Cancel after completion = %v, want ErrOrderSettled", err)
	}
}

// Confirming receipt needs evidence and happens once: it starts the payout clock and is what
// a later refund would be judged on.
func TestOrder_ConfirmReceipt(t *testing.T) {
	o := newOrder(t)
	// Nothing shipped before the seller accepted, so nothing can have arrived.
	if err := o.ConfirmReceipt([]int64{1}); !errors.Is(err, domain.ErrOrderNotConfirmed) {
		t.Fatalf("ConfirmReceipt before confirmation = %v, want ErrOrderNotConfirmed", err)
	}
	if err := o.Confirm(); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if err := o.ConfirmReceipt(nil); !errors.Is(err, domain.ErrReceiptNeedsEvidence) {
		t.Fatalf("ConfirmReceipt with nothing = %v, want ErrReceiptNeedsEvidence", err)
	}
	if err := o.ConfirmReceipt([]int64{1, 2}); err != nil {
		t.Fatalf("ConfirmReceipt: %v", err)
	}
	if o.ReceivedAt == nil || len(o.ReceiptAttachments) != 2 {
		t.Fatalf("order = %+v, want the receipt recorded", o)
	}
	if err := o.ConfirmReceipt([]int64{3}); !errors.Is(err, domain.ErrReceiptAlreadyConfirmed) {
		t.Fatalf("second ConfirmReceipt = %v, want ErrReceiptAlreadyConfirmed", err)
	}
	// The payout is due a window after the receipt, computed rather than stored.
	due := o.PayoutDue()
	if due == nil || due.Sub(*o.ReceivedAt) != domain.PayoutWindow {
		t.Fatalf("payout due = %v, want the receipt plus the window", due)
	}
}

// A parcel in transit cannot be un-sent: after it ships the buyer asks for a refund.
func TestOrder_CancelOnlyBeforeShipping(t *testing.T) {
	o := newOrder(t)
	if err := o.Cancel(true); !errors.Is(err, domain.ErrOrderNotCancellable) {
		t.Fatalf("Cancel of a shipped order = %v, want ErrOrderNotCancellable", err)
	}
	if err := o.Cancel(false); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if o.State() != domain.StateCancelled {
		t.Fatalf("state = %q, want cancelled", o.State())
	}
}

// The refund windows in one test: each non-terminal status names the party on the clock, which
// is what lets one pass advance all of them.
func TestRefund_WindowsAndVerdicts(t *testing.T) {
	r, err := domain.NewRefund(1, 7, "not as described", []int64{42})
	if err != nil {
		t.Fatalf("NewRefund: %v", err)
	}
	if r.Status != domain.RefundAwaitingSeller || r.DeadlineAt == nil {
		t.Fatalf("refund = %+v, want the seller on the clock", r)
	}

	// A seller cannot refuse it: their two moves are granting it and handing it to staff, and
	// letting the window pass hands it to staff as well. Either way the buyer is never asked to
	// chase a case they already opened — which is what losing them the money used to look like.
	raised := r
	if err := raised.Escalate(); err != nil {
		t.Fatalf("Escalate from the seller's window: %v", err)
	}
	if raised.Status != domain.RefundDisputed || raised.DeadlineAt != nil ||
		raised.SellerDecidedAt == nil {
		t.Fatalf("refund = %+v, want it with staff, nobody on the clock, and the seller's answer recorded", raised)
	}
	lapsed := r
	if err := lapsed.EscalateUnanswered(); err != nil {
		t.Fatalf("EscalateUnanswered: %v", err)
	}
	if lapsed.Status != domain.RefundDisputed || lapsed.DeadlineAt != nil {
		t.Fatalf("refund = %+v, want silence to reach staff too", lapsed)
	}

	// Accepting opens the return leg, and nobody is on the clock while a carrier has it.
	accepted := r
	if err := accepted.Accept(); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if accepted.Status != domain.RefundReturning || accepted.DeadlineAt != nil {
		t.Fatalf("refund = %+v, want it returning with no deadline", accepted)
	}
	if err := accepted.StartReturn(99); err != nil {
		t.Fatalf("StartReturn: %v", err)
	}
	if err := accepted.MarkReturned(); err != nil {
		t.Fatalf("MarkReturned: %v", err)
	}
	// The goods are back and the seller may escalate, with their own window.
	if accepted.Status != domain.RefundReturned || accepted.DeadlineAt == nil {
		t.Fatalf("refund = %+v, want the seller's inspection window", accepted)
	}
	if err := accepted.Settle(); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if !accepted.Settled() || accepted.DeadlineAt != nil {
		t.Fatalf("refund = %+v, want it settled with nobody on the clock", accepted)
	}
	if err := accepted.Settle(); !errors.Is(err, domain.ErrRefundSettled) {
		t.Fatalf("second Settle = %v, want ErrRefundSettled", err)
	}
}

// A verdict reads `ReturnedAt` rather than a round: the same "buyer wins" grants the refund
// while the goods are still with the buyer, and pays it once they are back.
func TestRefund_VerdictReadsWhetherTheGoodsCameBack(t *testing.T) {
	granted, err := domain.NewRefund(1, 7, "not as described", nil)
	if err != nil {
		t.Fatalf("NewRefund: %v", err)
	}
	// Only staff decide, and only a case they were asked about.
	if err := granted.Resolve(true); !errors.Is(err, domain.ErrRefundNotDisputed) {
		t.Fatalf("Resolve before escalation = %v, want ErrRefundNotDisputed", err)
	}
	if err := granted.Escalate(); err != nil {
		t.Fatalf("Escalate: %v", err)
	}
	if granted.Status != domain.RefundDisputed || granted.DeadlineAt != nil {
		t.Fatalf("refund = %+v, want it with staff and nobody on the clock", granted)
	}
	// A case staff hold is not the seller's to concede: conceding it reaches `returning` with no
	// verdict published, and the ticket that asked for one then has nothing to close it.
	if err := granted.Accept(); !errors.Is(err, domain.ErrNotAwaitingSeller) {
		t.Fatalf("Accept while disputed = %v, want ErrNotAwaitingSeller", err)
	}
	// Nothing has come back yet, so the buyer winning grants the refund and the goods travel.
	decided := granted.SellerDecidedAt
	if err := granted.Resolve(true); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if granted.Status != domain.RefundReturning || granted.DeadlineAt != nil {
		t.Fatalf("refund = %+v, want the goods on their way back", granted)
	}
	// The seller's answer stays as they gave it. Stamping the moderator's clock here would leave
	// a row whose rejection reason is the seller's words at a moment they never spoke.
	if granted.SellerDecidedAt != decided {
		t.Fatalf("seller_decided_at = %v, want the seller's own %v", granted.SellerDecidedAt, decided)
	}

	// The same verdict after the return arrived pays the buyer instead: there is nothing to ship.
	paid := granted
	if err := paid.StartReturn(99); err != nil {
		t.Fatalf("StartReturn: %v", err)
	}
	if err := paid.MarkReturned(); err != nil {
		t.Fatalf("MarkReturned: %v", err)
	}
	if err := paid.Escalate(); err != nil {
		t.Fatalf("seller Escalate: %v", err)
	}
	if err := paid.Resolve(true); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if paid.Status != domain.RefundAccepted {
		t.Fatalf("refund = %+v, want the buyer paid back", paid)
	}

	// A verdict for the seller is terminal, whichever situation it was in.
	refused := granted
	refused.Status = domain.RefundDisputed
	if err := refused.Resolve(false); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if refused.Status != domain.RefundRejected || refused.DeadlineAt != nil {
		t.Fatalf("refund = %+v, want it rejected with nobody on the clock", refused)
	}
	if err := refused.Resolve(true); !errors.Is(err, domain.ErrRefundNotDisputed) {
		t.Fatalf("second Resolve = %v, want ErrRefundNotDisputed", err)
	}
}

// Only two states can be escalated, and both are the seller's: their own review window, and a
// return they say is not what the buyer's evidence showed.
func TestRefund_EscalatableStates(t *testing.T) {
	r, err := domain.NewRefund(1, 7, "not as described", nil)
	if err != nil {
		t.Fatalf("NewRefund: %v", err)
	}
	returning := r
	if err := returning.Accept(); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	// A carrier has it; the seller's turn comes when the parcel arrives.
	if err := returning.Escalate(); !errors.Is(err, domain.ErrRefundNotEscalatable) {
		t.Fatalf("Escalate while returning = %v, want ErrRefundNotEscalatable", err)
	}
}

// Evidence is a set, not a log of submissions. Nothing stops a client naming the same
// resource twice — in one submission or in a later top-up — and two copies of one photo are
// not two pieces of evidence: the case is the record a verdict gets reached on, and one that
// looks like it carries four photos when it carries three misstates it.
func TestRefund_EvidenceHoldsEachResourceOnce(t *testing.T) {
	// A single submission naming one resource twice.
	r, err := domain.NewRefund(1, 7, "not as described", []int64{42, 42, 43})
	if err != nil {
		t.Fatalf("NewRefund: %v", err)
	}
	if got := r.Attachments; !slices.Equal(got, []int64{42, 43}) {
		t.Fatalf("attachments = %v, want [42 43]", got)
	}

	// A top-up re-naming one already on the case keeps the case as it was, and adds the rest.
	if err := r.AddAttachments([]int64{43, 44, 44}); err != nil {
		t.Fatalf("AddAttachments: %v", err)
	}
	if got := r.Attachments; !slices.Equal(got, []int64{42, 43, 44}) {
		t.Fatalf("attachments = %v, want [42 43 44]", got)
	}
}

// Negotiation only moves the price down. The asking price is already an offer to sell at it,
// so terms above it are a proposal with no reason to exist — the buyer would simply buy.
func TestOffer_NotAboveAskingPrice(t *testing.T) {
	const buyer, seller = int64(7), int64(8)
	const asking = int64(100_000)

	terms := func(total int64, quantity int64) domain.NewTerms {
		return domain.NewTerms{
			ListingID: 1, VariantID: 2, BuyerID: buyer, SellerID: seller,
			Quantity: quantity, Total: total, UnitPrice: asking,
		}
	}

	if _, err := domain.NewOffer(terms(120_000, 1), time.Hour); !errors.Is(err, domain.ErrOfferAboveAsking) {
		t.Fatalf("opening above asking = %v, want ErrOfferAboveAsking", err)
	}
	// Exactly the asking price is not above it.
	if _, err := domain.NewOffer(terms(asking, 1), time.Hour); err != nil {
		t.Fatalf("opening at asking: %v", err)
	}
	// The ceiling scales with the quantity, and the comparison is on the total: three units
	// at the asking price is fine, one đồng more is not.
	if _, err := domain.NewOffer(terms(3*asking, 3), time.Hour); err != nil {
		t.Fatalf("opening at asking for three: %v", err)
	}
	if _, err := domain.NewOffer(terms(3*asking+1, 3), time.Hour); !errors.Is(err, domain.ErrOfferAboveAsking) {
		t.Fatalf("opening a đồng above asking for three = %v, want ErrOfferAboveAsking", err)
	}

	// And the same ceiling on the answer, from the other side: a rule the counter route did
	// not hold would be one move away from being no rule.
	o, err := domain.NewOffer(terms(80_000, 1), time.Hour)
	if err != nil {
		t.Fatalf("NewOffer: %v", err)
	}
	now := time.Now()
	if err := o.Counter(seller, 1, 120_000, asking, "firm", now, time.Hour); !errors.Is(err, domain.ErrOfferAboveAsking) {
		t.Fatalf("countering above asking = %v, want ErrOfferAboveAsking", err)
	}
	if o.Total != 80_000 || o.AuthorID != buyer {
		t.Fatalf("offer = %+v, want the refused counter to have changed nothing", o)
	}

	// An asking price the caller could not resolve enforces nothing, rather than refusing
	// every negotiation on a listing whose price lookup came back empty.
	if _, err := domain.NewOffer(domain.NewTerms{
		ListingID: 1, VariantID: 2, BuyerID: buyer, SellerID: seller,
		Quantity: 1, Total: 999_000,
	}, time.Hour); err != nil {
		t.Fatalf("opening with no known asking price: %v", err)
	}
}

// A negotiation alternates: the side holding the standing proposal cannot answer itself, and
// only the buyer closes it.
func TestOffer_AlternatesAndOnlyBuyerAccepts(t *testing.T) {
	const buyer, seller = int64(7), int64(8)
	// The listing asks 150_000; every move below happens under that ceiling.
	const asking = int64(150_000)
	o, err := domain.NewOffer(domain.NewTerms{ListingID: 1, VariantID: 2, BuyerID: buyer, SellerID: seller, Quantity: 1, Total: 100_000, Reason: "", UnitPrice: asking}, time.Hour)
	if err != nil {
		t.Fatalf("NewOffer: %v", err)
	}
	now := time.Now()
	// The buyer opened it, so answering is the seller's move.
	if err := o.Counter(buyer, 1, 90_000, asking, "", now, time.Hour); !errors.Is(err, domain.ErrNotYourTurn) {
		t.Fatalf("countering one's own = %v, want ErrNotYourTurn", err)
	}
	// A counter may go *up* — that is the seller holding out — as long as it stays at or
	// under what the listing asks.
	if err := o.Counter(seller, 1, 120_000, asking, "firm", now, time.Hour); err != nil {
		t.Fatalf("Counter: %v", err)
	}
	if o.AuthorID != seller || o.Total != 120_000 {
		t.Fatalf("offer = %+v, want the seller's terms on the table", o)
	}
	// Nobody agrees to their own price: the standing proposal is the seller's, so it is the
	// buyer's to answer.
	if err := o.Accept(seller, now, time.Minute); !errors.Is(err, domain.ErrNotYourTurn) {
		t.Fatalf("seller accepting their own = %v, want ErrNotYourTurn", err)
	}
	if err := o.Accept(buyer, now, 30*time.Minute); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if o.Status != domain.OfferAccepted {
		t.Fatalf("status = %q, want accepted", o.Status)
	}
	// Agreeing restarts the clock, short: an agreed price is a frozen price.
	if !o.ExpiresAt.After(now) || o.ExpiresAt.After(now.Add(31*time.Minute)) {
		t.Fatalf("expires at %v, want a short window from %v", o.ExpiresAt, now)
	}
	// Only the buyer turns it into an order, and only once.
	if err := o.CheckoutBy(seller, now); !errors.Is(err, domain.ErrOnlyBuyerCheckout) {
		t.Fatalf("seller checking out = %v, want ErrOnlyBuyerCheckout", err)
	}
	if err := o.CheckoutBy(buyer, now); err != nil {
		t.Fatalf("CheckoutBy: %v", err)
	}
	// The claim is the status, taken before the session exists — so a second press finds the
	// terms already off the table rather than finding a session id.
	o.CheckOut()
	if o.Status != domain.OfferCheckedOut {
		t.Fatalf("status = %q, want checked-out", o.Status)
	}
	if err := o.CheckoutBy(buyer, now); !errors.Is(err, domain.ErrOfferSettled) {
		t.Fatalf("second checkout = %v, want ErrOfferSettled", err)
	}
	// And the expiry leaves a paying buyer alone: that clock is the payment session's.
	if err := o.Expire(); !errors.Is(err, domain.ErrOfferSettled) {
		t.Fatalf("expiring a checked-out offer = %v, want ErrOfferSettled", err)
	}

	// The other direction: a seller may agree to the buyer's price, and it is still the buyer who
	// pays — which is what makes that safe.
	fromBuyer, err := domain.NewOffer(domain.NewTerms{ListingID: 1, VariantID: 2, BuyerID: buyer, SellerID: seller, Quantity: 1, Total: 90_000, Reason: ""}, time.Hour)
	if err != nil {
		t.Fatalf("NewOffer: %v", err)
	}
	if err := fromBuyer.Accept(seller, now, 30*time.Minute); err != nil {
		t.Fatalf("seller accepting the buyer's price: %v", err)
	}
	if err := fromBuyer.CheckoutBy(seller, now); !errors.Is(err, domain.ErrOnlyBuyerCheckout) {
		t.Fatalf("seller checking out = %v, want ErrOnlyBuyerCheckout", err)
	}
	if err := fromBuyer.CheckoutBy(buyer, now); err != nil {
		t.Fatalf("buyer checking out agreed terms: %v", err)
	}

	// And an expired negotiation is not answerable at all.
	stale, err := domain.NewOffer(domain.NewTerms{ListingID: 1, VariantID: 2, BuyerID: buyer, SellerID: seller, Quantity: 1, Total: 100_000, Reason: ""}, -time.Hour)
	if err != nil {
		t.Fatalf("NewOffer: %v", err)
	}
	if err := stale.Accept(seller, now, time.Minute); !errors.Is(err, domain.ErrOfferExpired) {
		t.Fatalf("accepting an expired offer = %v, want ErrOfferExpired", err)
	}
}

// A draft freezes the terms: a variant it never carried cannot be bought through it, which is
// the whole point.
func TestDraft_FreezesItsVariants(t *testing.T) {
	snapshot := domain.ListingSnapshot{
		ListingID: 1, SellerID: 8, Name: "Ao thun", Currency: "VND", PriceMode: "fixed",
		Variants: []domain.VariantSnapshot{{VariantID: 2, Price: 100_000}},
	}
	d, err := domain.NewDraft(7, snapshot, time.Hour)
	if err != nil {
		t.Fatalf("NewDraft: %v", err)
	}
	if !d.Live(time.Now()) {
		t.Fatal("a fresh draft is not live")
	}
	frozen, err := d.Variant(2)
	if err != nil || frozen.Price != 100_000 {
		t.Fatalf("Variant = %+v, %v; want the frozen price", frozen, err)
	}
	if _, err := d.Variant(99); !errors.Is(err, domain.ErrVariantNotInDraft) {
		t.Fatalf("Variant of a stranger = %v, want ErrVariantNotInDraft", err)
	}
	if err := d.Cancel(); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if d.Live(time.Now()) {
		t.Fatal("a cancelled draft is still live")
	}
	if err := d.Cancel(); !errors.Is(err, domain.ErrDraftSettled) {
		t.Fatalf("second Cancel = %v, want ErrDraftSettled", err)
	}
}
