package domain

import (
	"time"

	"shopnexus/internal/shared/validation"
)

// Report enum values (kebab-case, mirror the report_* enums).
const (
	ReportRefListing     = "listing"
	ReportRefAccount     = "account"
	ReportRefMessage     = "message"
	ReportRefReview      = "review"
	ReportRefReviewReply = "review-reply"

	ReportStatusOpen      = "open"
	ReportStatusReviewing = "reviewing"
	ReportStatusActioned  = "actioned"
	ReportStatusDismissed = "dismissed"

	// What a moderator did about an upheld report. `none` goes with a dismissal.
	ActionNone             = "none"
	ActionListingRemoved   = "listing-removed"
	ActionMessageRemoved   = "message-removed"
	ActionAccountSuspended = "account-suspended"
	ActionWarning          = "warning"
)

// Report is an abuse report against any target. A reporter may only have one unresolved
// report per target, which a partial unique index holds rather than this code.
type Report struct {
	ID         int64
	ReporterID int64  `validate:"required"`
	RefType    string `validate:"required,oneof=listing account message review review-reply"`
	// RefID is polymorphic: which entity it points at is given by RefType. The service
	// decodes it from the opaque wire form with that type's prefix.
	RefID     int64  `validate:"required"`
	Reason    string `validate:"required,oneof=scam counterfeit prohibited harassment spam inappropriate other"`
	Detail    string `validate:"max=2000"`
	Status    string `validate:"required,oneof=open reviewing actioned dismissed"`
	CreatedAt time.Time

	// ActionTaken, ResolvedByID, ResolvedAt and ResolutionNote are the verdict, all nil
	// until one is recorded.
	ActionTaken    *string
	ResolvedByID   *int64
	ResolvedAt     *time.Time
	ResolutionNote *string
}

func NewReport(reporterID int64, refType string, refID int64, reason, detail string) (Report, error) {
	r := Report{
		ReporterID: reporterID,
		RefType:    refType,
		RefID:      refID,
		Reason:     reason,
		Detail:     detail,
		Status:     ReportStatusOpen,
	}
	if err := validation.Default().Struct(r); err != nil {
		return Report{}, validation.AsError(err)
	}
	return r, nil
}

// Resolved reports whether a verdict has been recorded.
func (r Report) Resolved() bool {
	return r.Status == ReportStatusActioned || r.Status == ReportStatusDismissed
}

// Claim takes an open report for review, so two moderators do not work the same case. Only
// from `open`: a claimed one is somebody's, and a resolved one is finished.
func (r *Report) Claim() error {
	if r.Status != ReportStatusOpen {
		return ErrReportNotClaimable
	}
	r.Status = ReportStatusReviewing
	return nil
}

// Resolve records the verdict and what was done about it. Recording is all it does —
// taking a listing down and suspending an account are calls to the modules that own them,
// so the decision and its effects each stay where they can be audited.
func (r *Report) Resolve(moderatorID int64, status, action, note string) error {
	if r.Resolved() {
		return ErrReportResolved
	}
	if status != ReportStatusActioned && status != ReportStatusDismissed {
		return ErrReportVerdictInvalid
	}
	// A dismissal did nothing by definition, and upholding a report that did nothing is a
	// verdict nobody can act on.
	if (status == ReportStatusDismissed) != (action == ActionNone) {
		return ErrReportActionMismatch
	}
	if !knownAction(action) {
		return ErrReportActionMismatch
	}
	r.Status = status
	r.ActionTaken = &action
	r.ResolvedByID = &moderatorID
	r.ResolvedAt = new(time.Now())
	if note != "" {
		r.ResolutionNote = &note
	}
	return nil
}

func knownAction(action string) bool {
	switch action {
	case ActionNone, ActionListingRemoved, ActionMessageRemoved, ActionAccountSuspended, ActionWarning:
		return true
	}
	return false
}
