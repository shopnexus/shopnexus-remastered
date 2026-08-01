// Package domain: the order module's entities and the rules that hold whatever calls them.
package domain

import (
	"time"

	"shopnexus/internal/shared/validation"
)

// Order states, derived rather than stored: the two outcome timestamps are the truth, and
// a status column would be a third fact to keep in step with them.
const (
	StateOpen      = "open"
	StateCompleted = "completed"
	StateCancelled = "cancelled"
)

// PayoutWindow is how long after receipt the money sits before it is released. It is the
// buyer's window to raise a refund, so it is a product decision rather than a technical
// one.
const PayoutWindow = 72 * time.Hour

// Order is the purchase, created as soon as the money lands — by the payment webhook, not
// by anybody pressing a button. A seller never approves an order: on a fixed-price listing
// there is nothing to approve, and on a negotiable one the only thing they can refuse is
// the price, which happens before this row exists.
type Order struct {
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
	default:
		return StateOpen
	}
}

// Settled reports whether the order has reached an outcome.
func (o Order) Settled() bool { return o.State() != StateOpen }

// ConfirmReceipt is the buyer saying the goods arrived, with the evidence a later dispute
// would be judged on. It starts the payout clock, which is why it is not re-openable.
func (o *Order) ConfirmReceipt(attachments []int64) error {
	if o.Settled() {
		return ErrOrderSettled
	}
	if o.ReceivedAt != nil {
		return ErrReceiptAlreadyConfirmed
	}
	if len(attachments) == 0 {
		return ErrReceiptNeedsEvidence
	}
	o.ReceivedAt = new(time.Now())
	o.ReceiptAttachments = attachments
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
	return nil
}

// Involves reports whether the account is a party to the order.
func (o Order) Involves(accountID int64) bool {
	return accountID != 0 && (o.BuyerID == accountID || o.SellerID == accountID)
}
