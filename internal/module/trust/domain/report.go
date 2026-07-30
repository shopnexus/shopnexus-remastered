package domain

import (
	"time"

	"shopnexus/internal/shared/validation"
)

// Report enum values (kebab-case, mirror the report_* enums).
const (
	ReportRefListing = "listing"
	ReportRefAccount = "account"
	ReportRefMessage = "message"
	ReportRefComment = "comment"

	ReportStatusOpen      = "open"
	ReportStatusReviewing = "reviewing"
	ReportStatusActioned  = "actioned"
	ReportStatusDismissed = "dismissed"
)

// Report is an abuse report against any target. A reporter may only have one
// unresolved report per target (enforced by a partial unique index).
type Report struct {
	ID         int64
	ReporterID int64  `validate:"required"`
	RefType    string `validate:"required,oneof=listing account message review review-reply"`
	// RefID is polymorphic: which entity it points at is given by RefType. The
	// service decodes it from the opaque wire form with that type's prefix.
	RefID     int64  `validate:"required"`
	Reason    string `validate:"required,oneof=scam counterfeit prohibited harassment spam inappropriate other"`
	Detail    string `validate:"max=2000"`
	Status    string `validate:"required,oneof=open reviewing actioned dismissed"`
	CreatedAt time.Time
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
