package domain

import (
	"net/http"

	"shopnexus/internal/shared/errx"
)

// Trust errors — not-found and app-level alike. They live here so the postgres adapter can
// return one without importing the module root.
var (
	// The ticket queue's refusals. One vocabulary for reports, refund disputes and support
	// requests, because they are one table.
	// ErrTicketTargetMissing is a ticket about something that is not there.
	ErrTicketTargetMissing = errx.NewError(http.StatusNotFound, "ticket_target_not_found", "what this ticket is about does not exist")
	ErrTicketNotFound      = errx.NewError(http.StatusNotFound, "ticket_not_found", "ticket not found")
	ErrTicketNotClaimable  = errx.NewError(http.StatusConflict, "ticket_not_claimable", "this ticket is already claimed or resolved")
	ErrTicketResolved      = errx.NewError(http.StatusConflict, "ticket_resolved", "this ticket is already resolved")
	ErrTicketActionInvalid = errx.NewError(http.StatusUnprocessableEntity, "ticket_action_invalid", "no such resolution action")
	// ErrTicketExists is a second open ticket about the same target: the same complaint twice.
	ErrTicketExists         = errx.NewError(http.StatusConflict, "ticket_exists", "you already have an open ticket about this")
	ErrTicketRefRequired    = errx.NewError(http.StatusUnprocessableEntity, "ticket_ref_required", "that kind of ticket has to name what it is about")
	ErrTicketRefUnexpected  = errx.NewError(http.StatusUnprocessableEntity, "ticket_ref_unexpected", "that kind of ticket is not about a particular thing")
	ErrTicketReasonMismatch = errx.NewError(http.StatusUnprocessableEntity, "ticket_reason_mismatch", "a reason belongs to a report and only to a report")
	// ErrTicketDecidedElsewhere is a refund dispute: the verdict moves money, so it is order's
	// route, and closing the ticket by hand here would leave the escrow where it was.
	ErrTicketDecidedElsewhere = errx.NewError(http.StatusConflict, "ticket_decided_elsewhere", "this ticket is resolved by deciding the refund it is about")

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

	// --- cross-cutting ---
	ErrModeratorRequired  = errx.NewError(http.StatusForbidden, "moderator_required", "moderator role required")
	ErrAttachmentNotFound = errx.NewError(http.StatusNotFound, "attachment_not_found", "an attachment names no confirmed upload")
)
