package domain

import (
	"slices"
	"time"

	"shopnexus/internal/shared/validation"
)

// Refund statuses (kebab-case, mirrors the refund_status enum). Every non-terminal value
// is named for the party whose move it waits on, which is what makes one timer able to
// advance all of them and "who is holding this up" answerable from the row.
const (
	RefundAwaitingSeller = "awaiting-seller-review"
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
	// SellerReviewWindow is how long the seller has to grant the refund or hand it to staff.
	// Letting it pass hands it to staff anyway — see EscalateUnanswered.
	SellerReviewWindow = 48 * time.Hour
	// SellerInspectionWindow is how long the seller has to escalate what came back before the
	// return settles for the buyer. This is the window that catches a buyer who returned a
	// broken item, or something other than what their evidence showed.
	SellerInspectionWindow = 48 * time.Hour
)

// MaxEvidence is how many photos one case carries, counted over the whole case rather than
// per submission. Ten is what a submission is capped at, and a top-up is not a way around
// that limit.
const MaxEvidence = 10

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

	ReturnTransportID *int64
	ReturnedAt        *time.Time
}

// NewRefund opens a case. It starts on the seller's clock: they get to accept or refuse
// before anybody else is involved.
func NewRefund(orderID, buyerID int64, reason string, attachments []int64) (Refund, error) {
	r := Refund{
		BuyerID: buyerID, OrderID: orderID, Reason: reason,
		Attachments: addEvidence(nil, attachments),
		Status:      RefundAwaitingSeller,
		DeadlineAt:  new(time.Now().Add(SellerReviewWindow)),
	}
	if len(r.Attachments) > MaxEvidence {
		return Refund{}, ErrTooMuchEvidence
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

// Accept is the seller granting it — one of their only two moves, the other being to hand the
// case to staff. The goods come back first: the return leg exists only from here, because a
// refund that never gets granted never ships anything. The money moves only once the parcel is
// back and the inspection window has passed, so granting is not paying.
//
// Only from their own window: a case that has been escalated is staff's to decide, and a seller
// conceding it would reach `returning` with no verdict published, which leaves trust holding a
// ticket nothing can ever close.
func (r *Refund) Accept() error {
	if r.Status != RefundAwaitingSeller {
		return ErrNotAwaitingSeller
	}
	r.SellerDecidedAt = new(time.Now())
	r.grantReturn()
	return nil
}

// grantReturn puts the goods on their way back. Both ways into `returning` — the seller granting
// it and a verdict for the buyer before the parcel moved — end here.
func (r *Refund) grantReturn() {
	r.Status = RefundReturning
	// A carrier decides how long this takes, so nobody is on the clock.
	r.DeadlineAt = nil
}

// EscalateUnanswered is the seller letting the review window pass. Staff take it over, exactly
// as if the seller had raised the ticket themselves.
//
// This is deliberately not "the buyer's turn to escalate", which is what it used to be. That
// put the burden on the party already out of pocket, and a buyer who did not know they had to
// act — or who simply did not open the app for three days — lost a case nobody had judged. A
// seller's silence is not a verdict.
func (r *Refund) EscalateUnanswered() error {
	if r.Status != RefundAwaitingSeller {
		return ErrNotAwaitingSeller
	}
	r.Status = RefundDisputed
	r.DeadlineAt = nil
	return nil
}

// Escalate hands the case to staff. Both entries belong to the seller: instead of refusing the
// refund on their own word they raise a ticket, and after the goods come back they may say that
// what arrived is not what the buyer's evidence showed. No deadline while staff hold it — a
// human decides when they decide.
//
// The buyer never escalates. They opened the case; asking them to open it a second time is the
// step that used to lose them the money.
func (r *Refund) Escalate() error {
	if r.Status != RefundAwaitingSeller && r.Status != RefundReturned {
		return ErrRefundNotEscalatable
	}
	if r.Status == RefundAwaitingSeller {
		r.SellerDecidedAt = new(time.Now())
	}
	r.Status = RefundDisputed
	r.DeadlineAt = nil
	return nil
}

// MarkReturned is the seller acknowledging the return arrived. They may then escalate what they
// received, and letting that window pass settles for the buyer — which is only fair because the
// report is the seller's own: having admitted receipt, their silence is what costs them.
func (r *Refund) MarkReturned() error {
	if r.Status != RefundReturning {
		return ErrRefundSettled
	}
	r.ReturnedAt = new(time.Now())
	r.Status = RefundReturned
	r.DeadlineAt = new(time.Now().Add(SellerInspectionWindow))
	return nil
}

// ClaimReturned is the buyer reporting the return arrived — a claim about somebody else's
// warehouse, so it asks staff for a verdict instead of opening the seller's inspection window.
// On that window a report only the buyer made pays them out as soon as the seller stops reading:
// one request plus 48 hours of somebody else's inattention, and the buyer had the money and the
// goods. Nobody else can contradict it either — the return leg is never booked with a carrier,
// so no webhook ever reports on it.
//
// `ReturnedAt` is stamped all the same: there is nothing left to ship, so a verdict for the buyer
// has to settle rather than grant a second parcel.
func (r *Refund) ClaimReturned() error {
	if r.Status != RefundReturning {
		return ErrRefundSettled
	}
	r.ReturnedAt = new(time.Now())
	r.Status = RefundDisputed
	r.DeadlineAt = nil
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
//
// `SellerDecidedAt` is left as the seller wrote it: it is when *they* answered, and stamping the
// moderator's clock on it would report the platform's decision as the seller's. Who decided, and
// why, is on the ticket the verdict closes.
func (r *Refund) Resolve(buyerWins bool) error {
	if r.Status != RefundDisputed {
		return ErrRefundNotDisputed
	}
	if buyerWins {
		if r.ReturnedAt != nil {
			return r.Settle()
		}
		r.grantReturn()
		return nil
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
	// Cloned so a refused batch cannot leave anything behind, and counted after the
	// deduplication: a top-up naming what the case already holds adds nothing, so it is not
	// past the cap however full the case is.
	next := addEvidence(slices.Clone(r.Attachments), attachments)
	if len(next) > MaxEvidence {
		return ErrTooMuchEvidence
	}
	r.Attachments = next
	return nil
}

// addEvidence appends the resources not already on the case, in the order they were
// submitted. Evidence is a set rather than a log of submissions: nothing stops a client
// naming the same resource twice — in one submission or in a later top-up — and two copies
// of one photo are not two pieces of evidence. Deduplicated here rather than in the request
// schema because a top-up cannot see what the case already holds, which is where the
// duplicate actually comes from.
func addEvidence(have, add []int64) []int64 {
	for _, key := range add {
		if !slices.Contains(have, key) {
			have = append(have, key)
		}
	}
	return have
}
