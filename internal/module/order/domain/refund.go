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
)

// The three windows a refund runs on. Named here rather than in a config file: they are
// product promises, and a buyer counting down is looking at the same numbers.
const (
	SellerReviewWindow = 48 * time.Hour
	BuyerActionWindow  = 72 * time.Hour
	SellerAppealWindow = 48 * time.Hour
)

// Dispute rulings (kebab-case, mirrors the dispute_status enum).
const (
	DisputeOpen       = "open"
	DisputeSellerWins = "seller-wins"
	DisputeBuyerWins  = "buyer-wins"
)

// Refund always covers the whole order, so there is no partial amount anywhere in the flow.
//
// The status is the state machine and the deadline is who is on the clock: 'disputed' waits
// on a moderator and 'returning' on a carrier, and neither is something a timer should
// decide, which is why those two carry no deadline.
type Refund struct {
	ID      int64
	BuyerID int64  `validate:"required"`
	OrderID int64  `validate:"required"`
	Reason  string `validate:"required,max=2000"`
	// Attachments are the buyer's evidence, added at creation and topped up until the case
	// closes: a dispute is decided on these, so they outlive the flow.
	Attachments []int64
	CreatedAt   time.Time
	Status      string `validate:"required"`
	DeadlineAt  *time.Time

	SellerDecidedAt *time.Time
	RejectionReason *string

	ReturnTransportID *int64
	ReturnedAt        *time.Time
	RefundTxID        *int64
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
	return r.Status == RefundAccepted || r.Status == RefundRejected
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

// Reject refuses it, with a reason. The buyer's move next: escalate to a moderator, or let
// the window lapse.
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

// Escalate is the buyer asking a moderator. A moderator has no deadline: a human decides
// when they decide.
func (r *Refund) Escalate() error {
	if r.Status != RefundAwaitingBuyer && r.Status != RefundReturned {
		return ErrNotAwaitingBuyer
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

// MarkReturned is the return leg arriving. The seller may then appeal — round two of the
// same dispute — and letting that window pass settles for the buyer.
func (r *Refund) MarkReturned() error {
	if r.Status != RefundReturning {
		return ErrRefundSettled
	}
	r.ReturnedAt = new(time.Now())
	r.Status = RefundReturned
	r.DeadlineAt = new(time.Now().Add(SellerAppealWindow))
	return nil
}

// Settle pays the buyer back. The reversal leg's id is recorded, which the schema only
// allows on an accepted refund.
func (r *Refund) Settle(refundTxID int64) error {
	if r.Settled() {
		return ErrRefundSettled
	}
	r.Status = RefundAccepted
	r.DeadlineAt = nil
	if refundTxID != 0 {
		r.RefundTxID = &refundTxID
	}
	return nil
}

// Rule applies a moderator's verdict, whichever round it is.
func (r *Refund) Rule(buyerWins bool) error {
	if r.Status != RefundDisputed {
		return ErrDisputeSettled
	}
	if buyerWins {
		// Round one grants the refund and the goods come back; round two is after they
		// already have, so there is nothing left to ship.
		if r.ReturnedAt != nil {
			return r.Settle(0)
		}
		return r.Accept()
	}
	r.Status = RefundRejected
	r.DeadlineAt = nil
	return nil
}

// StartReturn attaches the return leg. Buyer to seller, and there is no leg back: a seller
// who wins round one was never sent anything.
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

// Dispute is one round of moderation on a refund. Round two exists because a seller who
// receives goods back may say they are not what was sent.
type Dispute struct {
	ID        int64
	RefundID  int64  `validate:"required"`
	Round     int16  `validate:"required,gte=1"`
	OpenedBy  int64  `validate:"required"`
	Status    string `validate:"required,oneof=open seller-wins buyer-wins"`
	Reason    string
	RuledBy   *int64
	RuledAt   *time.Time
	Note      string
	CreatedAt time.Time
}

func NewDispute(refundID, openedBy int64, round int16, reason string) (Dispute, error) {
	d := Dispute{
		RefundID: refundID, Round: round, OpenedBy: openedBy,
		Status: DisputeOpen, Reason: reason,
	}
	if err := validation.Default().Struct(d); err != nil {
		return Dispute{}, validation.AsError(err)
	}
	return d, nil
}

// Rule records the verdict. A round is ruled once: re-deciding would rewrite the history a
// later round is argued against.
func (d *Dispute) Rule(moderatorID int64, buyerWins bool, note string) error {
	if d.Status != DisputeOpen {
		return ErrDisputeSettled
	}
	if buyerWins {
		d.Status = DisputeBuyerWins
	} else {
		d.Status = DisputeSellerWins
	}
	d.RuledBy = &moderatorID
	d.RuledAt = new(time.Now())
	d.Note = note
	return nil
}
