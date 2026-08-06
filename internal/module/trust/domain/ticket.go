package domain

import (
	"strings"
	"time"

	"shopnexus/internal/shared/validation"
)

// Ticket enum values (kebab-case, mirror the ticket_* enums).
const (
	// The kinds. Every one is the same shape — somebody submitted something and somebody answers in
	// a thread — which is why they are one table and one queue rather than a report table here, a
	// dispute table in order, and a support inbox nobody had built.
	KindReportListing     = "report-listing"
	KindReportAccount     = "report-account"
	KindReportMessage     = "report-message"
	KindReportReview      = "report-review"
	KindReportReviewReply = "report-review-reply"
	KindRefundDispute     = "refund-dispute"
	KindOrderIssue        = "order-issue"
	KindPayment           = "payment"
	KindAccount           = "account"
	KindFeatureRequest    = "feature-request"
	KindOther             = "other"

	// What a ticket is about, when it is about something.
	RefListing     = "listing"
	RefAccount     = "account"
	RefMessage     = "message"
	RefReview      = "review"
	RefReviewReply = "review-reply"
	RefOrder       = "order"
	RefRefund      = "refund"

	StatusOpen      = "open"
	StatusReviewing = "reviewing"
	StatusResolved  = "resolved"

	// What was done about it. `none` is a ticket looked at and turned down — the report table's old
	// `dismissed`, which was a second way of saying the same thing.
	ActionNone             = "none"
	ActionListingRemoved   = "listing-removed"
	ActionMessageRemoved   = "message-removed"
	ActionAccountSuspended = "account-suspended"
	ActionWarning          = "warning"
	ActionRefundGranted    = "refund-granted"
	ActionRefundRefused    = "refund-refused"
)

// refKindOf is which target each kind is about. A kind absent from it is about nothing in
// particular — a feature request, a question — and carries no ref at all.
var refKindOf = map[string]string{
	KindReportListing:     RefListing,
	KindReportAccount:     RefAccount,
	KindReportMessage:     RefMessage,
	KindReportReview:      RefReview,
	KindReportReviewReply: RefReviewReply,
	KindRefundDispute:     RefOrder,
	KindOrderIssue:        RefOrder,
}

// RefKindOf answers which target a kind points at, and "" when it points at nothing. The service
// decodes the opaque id with that type's prefix, which is what stops a listing id being accepted
// where a refund id belongs.
func RefKindOf(kind string) string { return refKindOf[kind] }

// Reported reports whether a kind is an abuse report — the ones that carry a reason and can end in a
// takedown, as opposed to a question or a dispute. Named one by one rather than matched on the
// `report-` prefix: a kind added to the enum and to nothing else must not silently inherit a
// vocabulary nobody wrote down for it.
func Reported(kind string) bool {
	switch kind {
	case KindReportListing, KindReportAccount, KindReportMessage, KindReportReview,
		KindReportReviewReply:
		return true
	}
	return false
}

// Ticket is one thing a user raised: an abuse report, a refund they want staff to decide, a payment
// that went wrong, a feature they wish existed.
//
// The conversation about it is chat's thread, named by ConversationID — so the requester's own words
// and photos are that thread's first message, and this row keeps neither a body nor an attachment
// list. A requester may only have one unresolved ticket per target, which a partial unique index
// holds rather than this code.
type Ticket struct {
	ID          int64
	RequesterID int64  `validate:"required"`
	Kind        string `validate:"required,oneof=report-listing report-account report-message report-review report-review-reply refund-dispute order-issue payment account feature-request other"`
	Subject     string `validate:"required,min=1,max=200"`
	// RefType and RefID are the polymorphic target, both nil on a ticket about nothing in
	// particular.
	RefType *string
	RefID   *int64
	// Reason is what a report says is wrong. Only the report kinds carry one.
	Reason *string `validate:"omitempty,oneof=scam counterfeit prohibited harassment spam inappropriate other"`
	Status string  `validate:"required,oneof=open reviewing resolved"`
	// AssigneeID is the moderator who claimed it, and is never published to the requester: support
	// answers as the desk, so a decision is the platform's rather than a named person's to be
	// argued with afterwards.
	AssigneeID *int64
	// ConversationID is chat's thread, nil until it exists: the ticket row is written first because
	// the two live in different schemas and only one of them can be.
	ConversationID *int64
	CreatedAt      time.Time

	// The verdict, all nil until one is recorded.
	ActionTaken    *string
	ResolvedByID   *int64
	ResolvedAt     *time.Time
	ResolutionNote *string
}

// NewTicket opens one. The target is checked against the kind: a report about a listing needs a
// listing, a refund dispute needs a refund, and a feature request needs nothing.
func NewTicket(requesterID int64, kind, subject string, refType *string, refID *int64, reason *string) (Ticket, error) {
	t := Ticket{
		RequesterID: requesterID,
		Kind:        kind,
		Subject:     strings.TrimSpace(subject),
		RefType:     refType,
		RefID:       refID,
		Reason:      reason,
		Status:      StatusOpen,
	}
	if err := validation.Default().Struct(t); err != nil {
		return Ticket{}, validation.AsError(err)
	}
	switch want := RefKindOf(kind); {
	case want == "" && refType != nil:
		return Ticket{}, ErrTicketRefUnexpected
	case want != "" && (refType == nil || *refType != want || refID == nil || *refID == 0):
		return Ticket{}, ErrTicketRefRequired
	}
	// A reason is the report kinds' vocabulary; anywhere else it is a field nobody reads.
	if Reported(kind) != (reason != nil) {
		return Ticket{}, ErrTicketReasonMismatch
	}
	return t, nil
}

// Resolved reports whether a verdict has been recorded.
func (t Ticket) Resolved() bool { return t.Status == StatusResolved }

// Claim takes an open ticket for review, so two moderators do not work the same case. Only from
// `open`: a claimed one is somebody's, and a resolved one is finished.
func (t *Ticket) Claim(moderatorID int64) error {
	if t.Status != StatusOpen {
		return ErrTicketNotClaimable
	}
	t.Status = StatusReviewing
	t.AssigneeID = &moderatorID
	return nil
}

// Resolve records the verdict and what was done about it. Recording is all it does — taking a
// listing down, suspending an account and moving money are calls to the modules that own those, so
// the decision and its effects each stay where they can be audited.
//
// `none` is the turn-down: read, answered, nothing done.
func (t *Ticket) Resolve(moderatorID int64, action, note string) error {
	if t.Resolved() {
		return ErrTicketResolved
	}
	if !knownAction(action) {
		return ErrTicketActionInvalid
	}
	t.Status = StatusResolved
	t.ActionTaken = &action
	t.ResolvedByID = &moderatorID
	t.ResolvedAt = new(time.Now())
	if note != "" {
		t.ResolutionNote = &note
	}
	return nil
}

// AttachThread records the conversation once it exists, and does nothing if it already knows one:
// the thread is created after the row, so the repair pass may find it already there.
func (t *Ticket) AttachThread(conversationID int64) {
	if t.ConversationID == nil {
		t.ConversationID = &conversationID
	}
}

func knownAction(action string) bool {
	switch action {
	case ActionNone, ActionListingRemoved, ActionMessageRemoved, ActionAccountSuspended,
		ActionWarning, ActionRefundGranted, ActionRefundRefused:
		return true
	}
	return false
}
