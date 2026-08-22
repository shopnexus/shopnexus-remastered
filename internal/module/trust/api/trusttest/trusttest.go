// Package trusttest provides a stub trustapi.Service for tests.
//
// A test that cares about one method should not have to write the rest. Embed Stub and
// override what the test is about; anything left over answers 501, so an unstubbed call shows
// up as an obviously wrong status rather than as a plausible zero value.
package trusttest

import (
	"context"

	"shopnexus/internal/module/common"
	trustapi "shopnexus/internal/module/trust/api"
	"shopnexus/internal/shared/errx"
)

// Stub implements trustapi.Service by refusing everything.
type Stub struct{}

var _ trustapi.Service = Stub{}

func (Stub) GetOrderFeedback(context.Context, trustapi.OrderFeedbackRequest) (trustapi.OrderFeedback, error) {
	return trustapi.OrderFeedback{}, errx.ErrNotImplemented
}

func (Stub) SubmitFeedback(context.Context, trustapi.SubmitFeedbackRequest) (trustapi.Feedback, error) {
	return trustapi.Feedback{}, errx.ErrNotImplemented
}

func (Stub) ListAccountFeedback(context.Context, trustapi.ListFeedbackRequest) (trustapi.FeedbackPage, error) {
	return trustapi.FeedbackPage{}, errx.ErrNotImplemented
}

func (Stub) GetReputation(context.Context, trustapi.GetReputationRequest) (trustapi.Reputation, error) {
	return trustapi.Reputation{}, errx.ErrNotImplemented
}

func (Stub) ListReviews(context.Context, trustapi.ListReviewsRequest) (trustapi.ReviewPage, error) {
	return trustapi.ReviewPage{}, errx.ErrNotImplemented
}

func (Stub) GetReviewSummary(context.Context, trustapi.GetReviewSummaryRequest) (trustapi.ReviewSummary, error) {
	return trustapi.ReviewSummary{}, errx.ErrNotImplemented
}

func (Stub) SubmitReview(context.Context, trustapi.SubmitReviewRequest) (trustapi.Review, error) {
	return trustapi.Review{}, errx.ErrNotImplemented
}

func (Stub) GetReview(context.Context, trustapi.GetReviewRequest) (trustapi.Review, error) {
	return trustapi.Review{}, errx.ErrNotImplemented
}

func (Stub) UpdateReview(context.Context, trustapi.UpdateReviewRequest) (trustapi.Review, error) {
	return trustapi.Review{}, errx.ErrNotImplemented
}

func (Stub) DeleteReview(context.Context, trustapi.ReviewRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) SubmitReply(context.Context, trustapi.SubmitReplyRequest) (trustapi.ReviewReply, error) {
	return trustapi.ReviewReply{}, errx.ErrNotImplemented
}

func (Stub) DeleteReply(context.Context, trustapi.ReplyRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) VoteReview(context.Context, trustapi.VoteReviewRequest) (trustapi.VoteTally, error) {
	return trustapi.VoteTally{}, errx.ErrNotImplemented
}

func (Stub) UnvoteReview(context.Context, trustapi.ReviewRequest) (trustapi.VoteTally, error) {
	return trustapi.VoteTally{}, errx.ErrNotImplemented
}

func (Stub) CreateUpload(context.Context, common.CreateUploadRequest) (common.UploadSlotDTO, error) {
	return common.UploadSlotDTO{}, errx.ErrNotImplemented
}

func (Stub) ConfirmUpload(context.Context, common.ConfirmUploadRequest) (common.ResourceDTO, error) {
	return common.ResourceDTO{}, errx.ErrNotImplemented
}

func (Stub) OpenTicket(context.Context, trustapi.OpenTicketRequest) (trustapi.Ticket, error) {
	return trustapi.Ticket{}, errx.ErrNotImplemented
}

func (Stub) ListMyTickets(context.Context, trustapi.ListTicketsRequest) (trustapi.TicketPage, error) {
	return trustapi.TicketPage{}, errx.ErrNotImplemented
}

func (Stub) GetTicket(context.Context, trustapi.TicketRequest) (trustapi.Ticket, error) {
	return trustapi.Ticket{}, errx.ErrNotImplemented
}

func (Stub) AdminListTickets(context.Context, trustapi.AdminListTicketsRequest) (trustapi.AdminTicketPage, error) {
	return trustapi.AdminTicketPage{}, errx.ErrNotImplemented
}

func (Stub) AdminGetTicket(context.Context, trustapi.TicketRequest) (trustapi.AdminTicket, error) {
	return trustapi.AdminTicket{}, errx.ErrNotImplemented
}

func (Stub) AdminClaimTicket(context.Context, trustapi.TicketRequest) (trustapi.Ticket, error) {
	return trustapi.Ticket{}, errx.ErrNotImplemented
}

func (Stub) RecordRefundVerdict(context.Context, trustapi.RecordRefundVerdictRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) AdminResolveTicket(context.Context, trustapi.ResolveTicketRequest) (trustapi.Ticket, error) {
	return trustapi.Ticket{}, errx.ErrNotImplemented
}

func (Stub) RevealDueFeedback(context.Context, int) (int, error) {
	return 0, errx.ErrNotImplemented
}

func (Stub) RecordOrderOutcome(context.Context, trustapi.RecordOrderOutcomeRequest) error {
	return errx.ErrNotImplemented
}
