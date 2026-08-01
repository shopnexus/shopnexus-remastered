package domain

import (
	"net/http"

	"shopnexus/internal/shared/errx"
)

// Trust errors — not-found and app-level alike. They live here so the postgres adapter can
// return one without importing the module root.
var (
	// --- feedback ---
	ErrFeedbackNotFound = errx.NewError(http.StatusNotFound, "feedback_not_found", "feedback not found")
	ErrFeedbackExists   = errx.NewError(http.StatusConflict, "feedback_exists", "this order was already rated in this direction")
	ErrSelfFeedback     = errx.NewError(http.StatusBadRequest, "self_feedback", "an account cannot rate itself")
	ErrNotAParty        = errx.NewError(http.StatusForbidden, "not_a_party", "you are not a party to this order")
	ErrOrderNotFinished = errx.NewError(http.StatusUnprocessableEntity, "order_not_finished", "this order is not finished yet")

	// --- reviews ---
	ErrReviewNotFound = errx.NewError(http.StatusNotFound, "review_not_found", "review not found")
	ErrReviewExists   = errx.NewError(http.StatusConflict, "review_exists", "you already reviewed this listing for this order")
	// ErrListingNotInOrder is a review of something the order did not carry: no purchase, no
	// review — and buying one thing does not earn a review of another.
	ErrListingNotInOrder = errx.NewError(http.StatusUnprocessableEntity, "listing_not_in_order", "this order did not include this listing")
	// ErrOrderNotCompleted is stricter than ErrOrderNotFinished, which feedback keeps: a
	// fully refunded order is finished, and its buyer has no goods left to review.
	ErrOrderNotCompleted = errx.NewError(http.StatusUnprocessableEntity, "order_not_completed", "only a completed order earns a review of its goods")
	ErrReviewRatingRange = errx.NewError(http.StatusBadRequest, "review_rating_range", "a rating is between 1 and 5")
	ErrReviewBodyTooLong = errx.NewError(http.StatusBadRequest, "review_body_too_long", "a review body is at most 2000 characters")
	ErrReviewForbidden   = errx.NewError(http.StatusForbidden, "review_forbidden", "only the author or a moderator may change this review")

	ErrReplyNotFound  = errx.NewError(http.StatusNotFound, "review_reply_not_found", "reply not found")
	ErrReplyForbidden = errx.NewError(http.StatusForbidden, "review_reply_forbidden", "only the author or a moderator may delete this reply")

	ErrVoteValue    = errx.NewError(http.StatusBadRequest, "vote_value", "a vote is 1 (helpful) or -1 (not helpful)")
	ErrSelfVote     = errx.NewError(http.StatusUnprocessableEntity, "self_vote", "an account cannot vote on its own review")
	ErrVoteNotFound = errx.NewError(http.StatusNotFound, "vote_not_found", "you have not voted on this review")

	// --- reports ---
	ErrReportNotFound = errx.NewError(http.StatusNotFound, "report_not_found", "report not found")
	ErrReportExists   = errx.NewError(http.StatusConflict, "report_exists", "you already have an unresolved report for this target")
	// ErrReportTargetNotFound is a report against something that does not exist, checked
	// against the module that owns the target so a typo'd id cannot fill the queue.
	ErrReportTargetNotFound = errx.NewError(http.StatusNotFound, "report_target_not_found", "the reported thing does not exist")
	ErrReportNotClaimable   = errx.NewError(http.StatusConflict, "report_not_claimable", "this report is already claimed or resolved")
	ErrReportResolved       = errx.NewError(http.StatusConflict, "report_resolved", "this report is already resolved")
	ErrReportVerdictInvalid = errx.NewError(http.StatusBadRequest, "report_verdict_invalid", "a verdict is actioned or dismissed")
	ErrReportActionMismatch = errx.NewError(http.StatusBadRequest, "report_action_mismatch", "a dismissal takes the action none, and upholding a report names what was done")

	// --- cross-cutting ---
	ErrModeratorRequired  = errx.NewError(http.StatusForbidden, "moderator_required", "moderator role required")
	ErrAttachmentNotFound = errx.NewError(http.StatusNotFound, "attachment_not_found", "an attachment names no confirmed upload")
)
