package domain

import (
	"time"

	"shopnexus/internal/shared/validation"
)

// Refund statuses (kebab-case, mirrors the refund_status enum). Every non-terminal value
// is named for the party whose move it waits on, which is what makes one timer able to
// advance all of them and "who is holding this up" answerable from the row.
const (
	RefundAwaitingSeller = "awaiting-seller-review"
	RefundAwaitingBuyer  = "awaiting-buyer-action"
	RefundDisputed       = "disputed"
	RefundReturning      = "returning"
	RefundReturned       = "returned"
	RefundAccepted       = "accepted"
	RefundRejected       = "rejected"
	// RefundCancelled is the buyer withdrawing before the seller decided. Its own terminal
	// value, because filing a withdrawal as a rejection makes it indistinguishable from a
	// seller who won.
	RefundCancelled = "cancelled"
)

// The three windows a refund runs on. Named here rather than in a config file: they are
// product promises, and a buyer counting down is looking at the same numbers.
const (
	SellerReviewWindow = 48 * time.Hour
	BuyerActionWindow  = 72 * time.Hour
	SellerAppealWindow = 48 * time.Hour
)

// Refund always covers the whole order, so there is no partial amount anywhere in the flow.
//
// The status is the state machine and the deadline is who is on the clock: 'disputed' waits
// on staff and 'returning' on a carrier, and neither is something a timer should decide,
// which is why those two carry no deadline.
type Refund struct {
	ID      int64
	BuyerID int64  `validate:"required"`
	OrderID int64  `validate:"required"`
	Reason  string `validate:"required,max=2000"`
	// Attachments are the buyer's evidence, added at creation and topped up until the case
	// closes: a verdict is reached on these, so they outlive the flow.
	Attachments []int64
	CreatedAt   time.Time
	Status      string `validate:"required"`
	DeadlineAt  *time.Time

	SellerDecidedAt *time.Time
	RejectionReason *string

	ReturnTransportID *int64
	ReturnedAt        *time.Time
}

// NewRefund opens a case. It starts on the seller's clock: they get to accept or refuse
// before anybody else is involved.
func NewRefund(orderID, buyerID int64, reason string, attachments []int64) (Refund, error) {
	r := Refund{
		BuyerID: buyerID, OrderID: orderID, Reason: reason, Attachments: attachments,
		Status: RefundAwaitingSeller, DeadlineAt: new(time.Now().Add(SellerReviewWindow)),
	}
	if err := validation.Default().Struct(r); err != nil {
		return Refund{}, validation.AsError(err)
	}
	return r, nil
}

// Settled reports whether the case is closed.
func (r Refund) Settled() bool {
	return r.Status == RefundAccepted || r.Status == RefundRejected ||
		r.Status == RefundCancelled
}

// Withdraw is the buyer dropping the case, and only while the seller has not decided: after
// that there is a verdict on the record and walking away would erase it.
func (r *Refund) Withdraw() error {
	if r.Status != RefundAwaitingSeller {
		return ErrRefundSettled
	}
	r.Status = RefundCancelled
	r.DeadlineAt = nil
	return nil
}

// Accept is the seller granting it. The goods come back first: the return leg exists only
// from here, because a refund that never gets granted never ships anything.
func (r *Refund) Accept() error {
	if r.Status != RefundAwaitingSeller && r.Status != RefundDisputed {
		return ErrNotAwaitingSeller
	}
	r.SellerDecidedAt = new(time.Now())
	r.Status = RefundReturning
	// A carrier decides how long this takes, so nobody is on the clock.
	r.DeadlineAt = nil
	return nil
}

// Reject refuses it, with a reason. The buyer's move next: escalate to staff, or let the
// window lapse.
func (r *Refund) Reject(reason string) error {
	if r.Status != RefundAwaitingSeller {
		return ErrNotAwaitingSeller
	}
	if reason == "" {
		return ErrRejectionNeedsReason
	}
	r.SellerDecidedAt = new(time.Now())
	r.RejectionReason = &reason
	r.Status = RefundAwaitingBuyer
	r.DeadlineAt = new(time.Now().Add(BuyerActionWindow))
	return nil
}

// LapseSellerReview is the seller letting the window pass. It lands on the buyer exactly
// as a rejection does, and the absent reason is what tells the two apart.
func (r *Refund) LapseSellerReview() error {
	if r.Status != RefundAwaitingSeller {
		return ErrNotAwaitingSeller
	}
	r.Status = RefundAwaitingBuyer
	r.DeadlineAt = new(time.Now().Add(BuyerActionWindow))
	return nil
}

// Escalate hands the case to staff: the buyer after a refusal, the seller after the goods
// came back. No deadline while they hold it — a human decides when they decide.
func (r *Refund) Escalate() error {
	if r.Status != RefundAwaitingBuyer && r.Status != RefundReturned {
		return ErrRefundNotEscalatable
	}
	r.Status = RefundDisputed
	r.DeadlineAt = nil
	return nil
}

// LapseBuyerAction is the buyer letting a rejection stand. The case closes for the seller.
func (r *Refund) LapseBuyerAction() error {
	if r.Status != RefundAwaitingBuyer {
		return ErrNotAwaitingBuyer
	}
	r.Status = RefundRejected
	r.DeadlineAt = nil
	return nil
}

// MarkReturned is the return leg arriving. The seller may then escalate what they received,
// and letting that window pass settles for the buyer.
func (r *Refund) MarkReturned() error {
	if r.Status != RefundReturning {
		return ErrRefundSettled
	}
	r.ReturnedAt = new(time.Now())
	r.Status = RefundReturned
	r.DeadlineAt = new(time.Now().Add(SellerAppealWindow))
	return nil
}

// Settle pays the buyer back. The ledger movement behind it is keyed on the order
// (`order:N:refund`), so the leg is found from finance rather than copied onto this row.
func (r *Refund) Settle() error {
	if r.Settled() {
		return ErrRefundSettled
	}
	r.Status = RefundAccepted
	r.DeadlineAt = nil
	return nil
}

// Resolve applies the staff verdict. There are no rounds: `ReturnedAt IS NULL` is what tells
// the two situations apart — a buyer who wins before the goods came back is granted the refund
// and they travel, and one who wins after has nothing left to ship, so the money moves.
func (r *Refund) Resolve(buyerWins bool) error {
	if r.Status != RefundDisputed {
		return ErrRefundNotDisputed
	}
	if buyerWins {
		if r.ReturnedAt != nil {
			return r.Settle()
		}
		return r.Accept()
	}
	r.Status = RefundRejected
	r.DeadlineAt = nil
	return nil
}

// StartReturn attaches the return leg. Buyer to seller, and there is no leg back: a seller
// who wins was never sent anything.
func (r *Refund) StartReturn(transportID int64) error {
	if r.Status != RefundReturning {
		return ErrRefundSettled
	}
	r.ReturnTransportID = &transportID
	return nil
}

// AddAttachments tops up the evidence while the case is open. A closed one is the record a
// verdict was reached on, so nothing is added to it afterwards.
func (r *Refund) AddAttachments(attachments []int64) error {
	if r.Settled() {
		return ErrRefundSettled
	}
	r.Attachments = append(r.Attachments, attachments...)
	return nil
}
