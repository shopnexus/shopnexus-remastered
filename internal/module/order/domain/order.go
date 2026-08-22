// Package domain: the order module's entities and the rules that hold whatever calls them.
package domain

import (
	"time"

	"shopnexus/internal/shared/validation"
)

// Order states, derived rather than stored: the outcome timestamps are the truth, and a
// status column would be one more fact to keep in step with them.
const (
	// StateAwaitingConfirmation is a paid order the seller has not accepted yet. Nothing has
	// been handed to a carrier in this state, which is the whole reason it exists.
	StateAwaitingConfirmation = "awaiting-confirmation"
	StateOpen                 = "open"
	StateCompleted            = "completed"
	StateCancelled            = "cancelled"
)

// PayoutWindow is how long after receipt the money sits before it is released. It is the
// buyer's window to raise a refund, so it is a product decision rather than a technical
// one.
const PayoutWindow = 72 * time.Hour

// SellerConfirmWindow is how long the seller has to accept the sale before staff are asked to
// chase it. The buyer's money is already held, so this cannot be open-ended.
const SellerConfirmWindow = 48 * time.Hour

// Order is the purchase, created as soon as the money lands — by the payment webhook, not by
// anybody pressing a button. But the *seller* is what starts it moving: the row exists and the
// escrow is held from payment, and nothing is handed to a carrier until they accept it. A
// listing whose stock is wrong, or whose owner has stopped selling, is otherwise discovered by
// the buyer waiting for a parcel nobody ever posted.
type Order struct {
	// events is what this order decided during the current command, drained by the repository
	// into the trail in the same transaction as the change.
	events []Event

	ID int64
	// Where the sale came from, exactly one of the two. A fixed-price listing is checked
	// out from a draft; a negotiable one is agreed in a negotiation.
	DraftID       *int64
	OfferID       *int64
	BuyerID       int64 `validate:"required"`
	SellerID      int64 `validate:"required"`
	TransportID   int64 `validate:"required"`
	Address       AddressSnapshot
	PickupAddress AddressSnapshot
	// ConfirmedAt is the seller accepting the sale, and the gate on the carrier ever hearing
	// about it. DeclineReason is set only on a refusal, which is a cancellation that says why —
	// reputation reads it, where a bare cancellation says nothing about who caused it.
	ConfirmedAt *time.Time
	// ConfirmationEscalatedAt is when staff were asked to chase the seller. A marker, not a
	// state: the order is still awaiting confirmation afterwards, and this only stops the sweep
	// raising it again.
	ConfirmationEscalatedAt *time.Time
	DeclineReason           *string
	// ReceivedAt and ReceiptAttachments are captured in the same request and never added
	// to afterwards: a refund is judged on what the buyer showed at the moment of
	// unboxing, so a growable list would weaken the record it exists to be.
	ReceivedAt         *time.Time
	ReceiptAttachments []int64
	// PayoutReleasedAt is when the escrow reached the seller. Nil on a completed order means
	// the outcome was written but the money never moved — a stranded release, which is a state
	// somebody has to see rather than one to forget.
	PayoutReleasedAt *time.Time
	CreatedAt        time.Time
	CompletedAt      *time.Time
	CancelledAt      *time.Time
}

func NewOrder(origin Origin, buyerID, sellerID, transportID int64, address, pickup AddressSnapshot) (Order, error) {
	if !origin.Valid() {
		return Order{}, ErrVariantNotInDraft
	}
	o := Order{
		DraftID: origin.DraftID, OfferID: origin.OfferID,
		BuyerID: buyerID, SellerID: sellerID, TransportID: transportID,
		Address: address, PickupAddress: pickup,
	}
	if err := validation.Default().Struct(o); err != nil {
		return Order{}, validation.AsError(err)
	}
	return o, nil
}

// State is read from the outcome timestamps rather than stored.
func (o Order) State() string {
	switch {
	case o.CancelledAt != nil:
		return StateCancelled
	case o.CompletedAt != nil:
		return StateCompleted
	case o.ConfirmedAt == nil:
		return StateAwaitingConfirmation
	default:
		return StateOpen
	}
}

// Confirm is the seller accepting the sale, and the only thing that lets the parcel be booked.
// Not re-runnable: a second confirmation would book a second parcel for one sale.
func (o *Order) Confirm() error {
	if o.Settled() {
		return ErrOrderSettled
	}
	if o.ConfirmedAt != nil {
		return ErrOrderAlreadyConfirmed
	}
	o.ConfirmedAt = new(time.Now())
	record(o, Confirmed, NoPayload{})
	return nil
}

// Decline is the seller refusing outright — out of stock, wrong price, whatever they say. It is
// the same outcome as letting the window pass, reached sooner: the sale is cancelled and the
// buyer is made whole. Only before confirming; after that the parcel is the seller's problem
// and the buyer's remedy is a refund.
func (o *Order) Decline(reason string) error {
	if o.Settled() {
		return ErrOrderSettled
	}
	if o.ConfirmedAt != nil {
		return ErrOrderAlreadyConfirmed
	}
	if reason == "" {
		return ErrDeclineNeedsReason
	}
	o.CancelledAt = new(time.Now())
	o.DeclineReason = &reason
	record(o, Declined, Refusal{Reason: reason})
	return nil
}

// EscalateConfirmation records that staff have been asked to chase the seller. It is not a
// transition: the order stays awaiting confirmation, because the platform will neither void the
// sale nor post the goods on the seller's behalf. Idempotent, so a sweep and a durable run
// racing each other raise it once.
func (o *Order) EscalateConfirmation() error {
	if o.Settled() {
		return ErrOrderSettled
	}
	if o.ConfirmedAt != nil {
		return ErrOrderAlreadyConfirmed
	}
	if o.ConfirmationEscalatedAt != nil {
		return ErrConfirmationAlreadyEscalated
	}
	o.ConfirmationEscalatedAt = new(time.Now())
	record(o, ConfirmationEscalated, NoPayload{})
	return nil
}

// Confirmed reports whether the seller has accepted the sale.
func (o Order) Confirmed() bool { return o.ConfirmedAt != nil }

// ConfirmationDue is when the seller runs out of time to accept, and nil once they have —
// computed rather than stored, so changing the window needs no migration.
func (o Order) ConfirmationDue() *time.Time {
	if o.ConfirmedAt != nil || o.Settled() {
		return nil
	}
	return new(o.CreatedAt.Add(SellerConfirmWindow))
}

// Settled reports whether the order has reached an outcome. Read off the two outcome
// timestamps rather than off State(), which now has a fourth value: `awaiting-confirmation` is
// the *earliest* point in an order's life, and defining settled as "not open" made every
// unconfirmed order look finished — so nothing could be confirmed at all.
func (o Order) Settled() bool { return o.CancelledAt != nil || o.CompletedAt != nil }

// ConfirmReceipt is the buyer saying the goods arrived, with the evidence a later refund
// would be judged on. It starts the payout clock, which is why it is not re-openable.
func (o *Order) ConfirmReceipt(attachments []int64) error {
	if o.Settled() {
		return ErrOrderSettled
	}
	// Nothing was shipped before the seller accepted, so there is nothing that could have
	// arrived. The database holds this too.
	if o.ConfirmedAt == nil {
		return ErrOrderNotConfirmed
	}
	if o.ReceivedAt != nil {
		return ErrReceiptAlreadyConfirmed
	}
	if len(attachments) == 0 {
		return ErrReceiptNeedsEvidence
	}
	o.ReceivedAt = new(time.Now())
	o.ReceiptAttachments = attachments
	record(o, Received, Receipt{Evidence: len(attachments)})
	return nil
}

// PayoutDue is when the escrow may be released: the receipt plus the window, unless a
// refund intervenes. Computed rather than stored, so changing the window does not need a
// migration of every open order.
func (o Order) PayoutDue() *time.Time {
	if o.ReceivedAt == nil {
		return nil
	}
	return new(o.ReceivedAt.Add(PayoutWindow))
}

// MarkPayoutReleased records that the escrow reached the seller. Separate from Complete so a
// release that failed leaves a completed order with no release time — which is exactly the list
// the retry pass reads, and the reason it does not have to ask finance about every sale that
// ever completed.
func (o *Order) MarkPayoutReleased() {
	if o.PayoutReleasedAt == nil {
		o.PayoutReleasedAt = new(time.Now())
	}
}

// Cancel voids an order before it ships. After that the buyer asks for a refund: a parcel
// in transit cannot be un-sent.
func (o *Order) Cancel(shipped bool) error {
	if o.Settled() {
		return ErrOrderSettled
	}
	if shipped {
		return ErrOrderNotCancellable
	}
	o.CancelledAt = new(time.Now())
	record(o, Cancelled, NoPayload{})
	return nil
}

// Involves reports whether the account is a party to the order.
func (o Order) Involves(accountID int64) bool {
	return accountID != 0 && (o.BuyerID == accountID || o.SellerID == accountID)
}
