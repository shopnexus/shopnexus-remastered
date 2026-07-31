// Package trust implements trustapi.Service.
package trust

import (
	"context"
	"fmt"
	"log/slog"

	trustapi "shopnexus/internal/module/trust/api"
	"shopnexus/internal/module/trust/domain"
	"shopnexus/internal/module/trust/port"
	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/id"
)

// prefixFor maps a report's ref_type to the id prefix that decodes its ref_id.
// The polymorphic reference stays a string on the wire because its kind is only
// known at run time; these are the values report_ref_type accepts.
func prefixFor(refType string) (string, bool) {
	switch refType {
	case "listing":
		return id.Prefix[id.Listing](), true
	case "account":
		return id.Prefix[id.Account](), true
	case "message":
		return id.Prefix[id.Message](), true
	case "review":
		return id.Prefix[id.Review](), true
	case "review-reply":
		return id.Prefix[id.ReviewReply](), true
	}
	return "", false
}

type Service struct {
	repo port.Repository
	log  *slog.Logger
}

func NewService(repo port.Repository, log *slog.Logger) *Service {
	return &Service{repo: repo, log: log}
}

var _ trustapi.Service = (*Service)(nil)

func (s *Service) SubmitFeedback(ctx context.Context, req trustapi.SubmitFeedbackRequest) (trustapi.Feedback, error) {
	f, err := domain.NewFeedback(req.OrderID.Int64(), req.RaterID.Int64(), req.RateeID.Int64(), req.Direction, req.Rating, req.Comment)
	if err != nil {
		return trustapi.Feedback{}, err
	}
	if err := s.repo.InsertFeedback(ctx, &f); err != nil {
		return trustapi.Feedback{}, fmt.Errorf("insert feedback: %w", err)
	}
	return trustapi.Feedback{
		ID:        id.Of[id.Feedback](f.ID),
		OrderID:   id.Of[id.Order](f.OrderID),
		RateeID:   id.Of[id.Account](f.RateeID),
		Direction: f.Direction,
		Rating:    f.Rating,
		Comment:   f.Comment,
		Published: f.Published(),
	}, nil
}

func (s *Service) GetReputation(ctx context.Context, req trustapi.GetReputationRequest) (trustapi.Reputation, error) {
	rep, err := s.repo.FindReputation(ctx, req.AccountID.Int64(), req.Role)
	if err != nil {
		return trustapi.Reputation{}, fmt.Errorf("find reputation: %w", err)
	}
	return trustapi.Reputation{
		AccountID:       id.Of[id.Account](rep.AccountID),
		Role:            rep.Role,
		AverageRating:   rep.AverageRating(),
		RatingCount:     rep.RatingCount,
		CompletedOrders: rep.CompletedOrders,
		CancelledOrders: rep.CancelledOrders,
	}, nil
}

func (s *Service) SubmitReport(ctx context.Context, req trustapi.SubmitReportRequest) (trustapi.Report, error) {
	prefix, ok := prefixFor(req.RefType)
	if !ok {
		return trustapi.Report{}, errx.NewValidationError("unknown ref_type "+req.RefType,
			errx.Field{Field: "ref_type", Rule: "oneof", Message: "must be a known report target type"})
	}
	refID, err := id.ParseOpaque(prefix, req.RefID)
	if err != nil {
		return trustapi.Report{}, err
	}
	r, err := domain.NewReport(req.ReporterID.Int64(), req.RefType, refID, req.Reason, req.Detail)
	if err != nil {
		return trustapi.Report{}, err
	}
	if err := s.repo.InsertReport(ctx, &r); err != nil {
		return trustapi.Report{}, fmt.Errorf("insert report: %w", err)
	}
	return trustapi.Report{
		ID:      id.Of[id.Report](r.ID),
		RefType: r.RefType,
		RefID:   id.FormatOpaque(prefix, r.RefID),
		Reason:  r.Reason,
		Status:  r.Status,
	}, nil
}
