package domain_test

import (
	"errors"
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

// The state is read from the two outcome timestamps rather than stored, so there is no third
// fact to keep in step with them.
func TestOrder_StateIsDerived(t *testing.T) {
	o := newOrder(t)
	if o.State() != domain.StateOpen || o.Settled() {
		t.Fatalf("state = %q, want open", o.State())
	}
	if err := o.ConfirmReceipt([]int64{42}); err != nil {
		t.Fatalf("ConfirmReceipt: %v", err)
	}
	if err := o.Complete(); err != nil {
		t.Fatalf("Complete: %v", err)
	}
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

// A payout only follows a confirmed receipt: money is released against a delivery, not
// against a promise.
func TestOrder_CompleteNeedsAReceipt(t *testing.T) {
	o := newOrder(t)
	if err := o.Complete(); !errors.Is(err, domain.ErrOrderNotCancellable) {
		t.Fatalf("Complete with no receipt = %v, want it refused", err)
	}
}

// The three refund windows in one test: each non-terminal status names the party on the clock,
// which is what lets one pass advance all of them.
func TestRefund_WindowsAndVerdicts(t *testing.T) {
	r, err := domain.NewRefund(1, 7, "not as described", []int64{42})
	if err != nil {
		t.Fatalf("NewRefund: %v", err)
	}
	if r.Status != domain.RefundAwaitingSeller || r.DeadlineAt == nil {
		t.Fatalf("refund = %+v, want the seller on the clock", r)
	}

	// A rejection lands on the buyer with a reason; a lapse lands there without one, and the
	// absent reason is what tells them apart.
	rejected := r
	if err := rejected.Reject(""); !errors.Is(err, domain.ErrRejectionNeedsReason) {
		t.Fatalf("Reject with no reason = %v", err)
	}
	if err := rejected.Reject("sent as described"); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if rejected.Status != domain.RefundAwaitingBuyer || rejected.RejectionReason == nil {
		t.Fatalf("refund = %+v, want the buyer on the clock with a reason", rejected)
	}
	lapsed := r
	if err := lapsed.LapseSellerReview(); err != nil {
		t.Fatalf("LapseSellerReview: %v", err)
	}
	if lapsed.Status != domain.RefundAwaitingBuyer || lapsed.RejectionReason != nil {
		t.Fatalf("refund = %+v, want the buyer on the clock with no reason", lapsed)
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
	// The goods are back and the seller may appeal — round two — with their own window.
	if accepted.Status != domain.RefundReturned || accepted.DeadlineAt == nil {
		t.Fatalf("refund = %+v, want the seller's appeal window", accepted)
	}
	if err := accepted.Settle(7); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if !accepted.Settled() || accepted.RefundTxID == nil {
		t.Fatalf("refund = %+v, want it settled with a reversal leg", accepted)
	}
	if err := accepted.Settle(8); !errors.Is(err, domain.ErrRefundSettled) {
		t.Fatalf("second Settle = %v, want ErrRefundSettled", err)
	}
}

// A dispute is ruled once: a later round is argued against what the earlier one decided.
func TestDispute_RuledOnce(t *testing.T) {
	d, err := domain.NewDispute(1, 7, 1, "seller refused")
	if err != nil {
		t.Fatalf("NewDispute: %v", err)
	}
	if err := d.Rule(99, true, "photos match the claim"); err != nil {
		t.Fatalf("Rule: %v", err)
	}
	if d.Status != domain.DisputeBuyerWins || d.RuledAt == nil || d.RuledBy == nil {
		t.Fatalf("dispute = %+v, want a recorded verdict", d)
	}
	if err := d.Rule(99, false, "changed my mind"); !errors.Is(err, domain.ErrDisputeSettled) {
		t.Fatalf("second Rule = %v, want ErrDisputeSettled", err)
	}
}

// A negotiation alternates: the side holding the standing proposal cannot answer itself, and
// only the buyer closes it.
func TestOffer_AlternatesAndOnlyBuyerAccepts(t *testing.T) {
	const buyer, seller = int64(7), int64(8)
	o, err := domain.NewOffer(1, 2, buyer, buyer, seller, 1, 100_000, "", time.Hour)
	if err != nil {
		t.Fatalf("NewOffer: %v", err)
	}
	now := time.Now()
	// The buyer opened it, so answering is the seller's move.
	if err := o.Counter(buyer, 1, 90_000, "", now, time.Hour); !errors.Is(err, domain.ErrNotYourTurn) {
		t.Fatalf("countering one's own = %v, want ErrNotYourTurn", err)
	}
	if err := o.Counter(seller, 1, 120_000, "firm", now, time.Hour); err != nil {
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
	if err := o.CheckoutBy(seller, now); !errors.Is(err, domain.ErrOnlyBuyerAccepts) {
		t.Fatalf("seller checking out = %v, want ErrOnlyBuyerAccepts", err)
	}
	if err := o.CheckoutBy(buyer, now); err != nil {
		t.Fatalf("CheckoutBy: %v", err)
	}
	o.PaymentSessionID = new(int64(7))
	if err := o.CheckoutBy(buyer, now); !errors.Is(err, domain.ErrOfferSettled) {
		t.Fatalf("second checkout = %v, want ErrOfferSettled", err)
	}

	// The other direction: a seller may agree to the buyer's price, and it is still the buyer who
	// pays — which is what makes that safe.
	fromBuyer, err := domain.NewOffer(1, 2, buyer, buyer, seller, 1, 90_000, "", time.Hour)
	if err != nil {
		t.Fatalf("NewOffer: %v", err)
	}
	if err := fromBuyer.Accept(seller, now, 30*time.Minute); err != nil {
		t.Fatalf("seller accepting the buyer's price: %v", err)
	}
	if err := fromBuyer.CheckoutBy(seller, now); !errors.Is(err, domain.ErrOnlyBuyerAccepts) {
		t.Fatalf("seller checking out = %v, want ErrOnlyBuyerAccepts", err)
	}
	if err := fromBuyer.CheckoutBy(buyer, now); err != nil {
		t.Fatalf("buyer checking out agreed terms: %v", err)
	}

	// And an expired negotiation is not answerable at all.
	stale, err := domain.NewOffer(1, 2, buyer, buyer, seller, 1, 100_000, "", -time.Hour)
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
