// Package trusttest provides a stub trustapi.Service for tests.
//
// A test that cares about one method should not have to write the rest. Embed Stub and
// override what the test is about; anything left over answers 501, so an unstubbed call shows
// up as an obviously wrong status rather than as a plausible zero value.
package trusttest

import (
	"context"

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

func (Stub) SubmitReport(context.Context, trustapi.SubmitReportRequest) (trustapi.Report, error) {
	return trustapi.Report{}, errx.ErrNotImplemented
}

func (Stub) ListMyReports(context.Context, trustapi.ListReportsRequest) (trustapi.ReportPage, error) {
	return trustapi.ReportPage{}, errx.ErrNotImplemented
}

func (Stub) AdminListReports(context.Context, trustapi.AdminListReportsRequest) (trustapi.AdminReportPage, error) {
	return trustapi.AdminReportPage{}, errx.ErrNotImplemented
}

func (Stub) AdminGetReport(context.Context, trustapi.ReportRequest) (trustapi.AdminReport, error) {
	return trustapi.AdminReport{}, errx.ErrNotImplemented
}

func (Stub) AdminClaimReport(context.Context, trustapi.ReportRequest) (trustapi.Report, error) {
	return trustapi.Report{}, errx.ErrNotImplemented
}

func (Stub) AdminResolveReport(context.Context, trustapi.ResolveReportRequest) (trustapi.Report, error) {
	return trustapi.Report{}, errx.ErrNotImplemented
}

func (Stub) RevealDueFeedback(context.Context, int) (int, error) {
	return 0, errx.ErrNotImplemented
}

func (Stub) RecordOrderOutcome(context.Context, trustapi.RecordOrderOutcomeRequest) error {
	return errx.ErrNotImplemented
}
