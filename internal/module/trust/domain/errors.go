package domain

import (
	"net/http"

	"shopnexus/internal/shared/errx"
)

// Trust errors. Not-found lives here so the postgres adapter can produce it
// without importing the module root package.
var (
	ErrFeedbackNotFound   = errx.NewError(http.StatusNotFound, "feedback_not_found", "feedback not found")
	ErrReputationNotFound = errx.NewError(http.StatusNotFound, "reputation_not_found", "reputation not found")
	ErrFeedbackExists     = errx.NewError(http.StatusConflict, "feedback_exists", "this order was already rated in this direction")
	ErrSelfFeedback       = errx.NewError(http.StatusBadRequest, "self_feedback", "an account cannot rate itself")
	ErrReportExists       = errx.NewError(http.StatusConflict, "report_exists", "you already have an unresolved report for this target")
)
