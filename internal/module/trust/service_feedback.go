package trust

import (
	"context"
	"fmt"
	"time"

	accountapi "shopnexus/internal/module/account/api"
	orderapi "shopnexus/internal/module/order/api"
	trustapi "shopnexus/internal/module/trust/api"
	"shopnexus/internal/module/trust/domain"
	"shopnexus/internal/module/trust/port"
	"shopnexus/internal/shared/id"
)

// GetOrderFeedback answers what the caller may see: their own submission always, the
// counterparty's only once published. While theirs is blind the client still learns that
// something is waiting and when it reveals, which is as much as can be said without
// breaking blindness.
func (s *Service) GetOrderFeedback(ctx context.Context, req trustapi.OrderFeedbackRequest) (trustapi.OrderFeedback, error) {
	if err := s.v.Struct(req); err != nil {
		return trustapi.OrderFeedback{}, err
	}
	order, err := s.orders.GetOrder(ctx, orderapi.OrderRequest{ActorID: req.ActorID, ID: req.OrderID})
	if err != nil {
		return trustapi.OrderFeedback{}, fmt.Errorf("read order: %w", err)
	}
	direction, _, err := domain.DirectionFor(req.ActorID.Int64(),
		order.Buyer.ID.Int64(), order.Seller.ID.Int64())
	if err != nil {
		return trustapi.OrderFeedback{}, err
	}
	rows, err := s.repo.OrderFeedback(ctx, req.OrderID.Int64())
	if err != nil {
		return trustapi.OrderFeedback{}, fmt.Errorf("read order feedback: %w", err)
	}

	var out trustapi.OrderFeedback
	var reveal *time.Time
	for _, row := range rows {
		view, err := s.feedbackView(ctx, row)
		if err != nil {
			return trustapi.OrderFeedback{}, err
		}
		switch {
		case row.Direction == direction:
			out.Mine = &view
		default:
			out.TheirsSubmitted = true
			if row.Published() {
				out.Theirs = &view
			}
		}
		// The soonest pending reveal is the moment something becomes visible.
		if at := row.RevealAt(); at != nil && (reveal == nil || at.Before(*reveal)) {
			reveal = at
		}
	}
	out.RevealAt = reveal
	return out, nil
}

// SubmitFeedback rates the counterparty of a finished order. One submission per direction, so
// it cannot be revised: a rating that could be edited after reading the other side is not
// blind.
func (s *Service) SubmitFeedback(ctx context.Context, req trustapi.SubmitFeedbackRequest) (trustapi.Feedback, error) {
	if err := s.v.Struct(req); err != nil {
		return trustapi.Feedback{}, err
	}
	order, err := s.orders.GetOrder(ctx, orderapi.OrderRequest{ActorID: req.ActorID, ID: req.OrderID})
	if err != nil {
		return trustapi.Feedback{}, fmt.Errorf("read order: %w", err)
	}
	// Both directions are only meaningful once the sale is over: a rating filed mid-delivery
	// rates something that has not happened.
	if order.State == "open" {
		return trustapi.Feedback{}, domain.ErrOrderNotFinished
	}
	direction, rateeID, err := domain.DirectionFor(req.ActorID.Int64(),
		order.Buyer.ID.Int64(), order.Seller.ID.Int64())
	if err != nil {
		return trustapi.Feedback{}, err
	}
	f, err := domain.NewFeedback(req.OrderID.Int64(), req.ActorID.Int64(), rateeID,
		direction, req.Rating, req.Comment)
	if err != nil {
		return trustapi.Feedback{}, err
	}
	if err := s.repo.InsertFeedback(ctx, &f); err != nil {
		return trustapi.Feedback{}, fmt.Errorf("insert feedback: %w", err)
	}
	return s.feedbackView(ctx, f)
}

// ListAccountFeedback is the published feedback an account received. A blind submission is
// visible to nobody but its author, so the filter is the repository's rather than a caller's.
func (s *Service) ListAccountFeedback(ctx context.Context, req trustapi.ListFeedbackRequest) (trustapi.FeedbackPage, error) {
	if err := s.v.Struct(req); err != nil {
		return trustapi.FeedbackPage{}, err
	}
	// A 404 for an account that does not exist, rather than an empty page that reads as
	// "nobody has rated them".
	if _, err := s.accounts.GetPublicAccount(ctx, accountapi.GetPublicAccountRequest{ID: req.AccountID}); err != nil {
		return trustapi.FeedbackPage{}, fmt.Errorf("read account: %w", err)
	}
	cursor, err := cursorFilter(req.Cursor, req.Limit)
	if err != nil {
		return trustapi.FeedbackPage{}, err
	}
	rows, err := s.repo.ListFeedback(ctx, port.FeedbackFilter{
		RateeID: req.AccountID.Int64(), Role: req.Role, Cursor: cursor,
	})
	if err != nil {
		return trustapi.FeedbackPage{}, fmt.Errorf("list feedback: %w", err)
	}
	rows, meta := paginate(rows, req.Limit, func(f domain.Feedback) time.Time { return f.CreatedAt })

	raters := make([]int64, 0, len(rows))
	for _, row := range rows {
		raters = append(raters, row.RaterID)
	}
	names := s.summaries(ctx, raters)
	data := make([]trustapi.Feedback, 0, len(rows))
	for _, row := range rows {
		data = append(data, toAPIFeedback(row, names[row.RaterID]))
	}
	return trustapi.FeedbackPage{Data: data, Meta: meta}, nil
}

// RevealDueFeedback publishes blind ratings whose window has run out, so a party who simply
// never rates cannot keep the other's rating hidden for ever. Idempotent: the publish is
// guarded by `published_at IS NULL`, so a retried pass counts nothing twice.
func (s *Service) RevealDueFeedback(ctx context.Context, limit int) (int, error) {
	rows, err := s.repo.DueFeedback(ctx, time.Now(), limit)
	if err != nil {
		return 0, fmt.Errorf("read due feedback: %w", err)
	}
	now := time.Now()
	published := 0
	for _, row := range rows {
		if err := s.repo.PublishFeedback(ctx, row.ID, now); err != nil {
			s.log.Error("publish feedback", "feedback_id", row.ID, "err", err)
			continue
		}
		published++
	}
	return published, nil
}

// RecordOrderOutcome folds a finished order into both parties' counters. Driven by order's
// settled event, not by a route — order is the authority and this is a mirror, so a recount
// repairs it rather than the consumer being clever.
func (s *Service) RecordOrderOutcome(ctx context.Context, req trustapi.RecordOrderOutcomeRequest) error {
	if err := s.v.Struct(req); err != nil {
		return err
	}
	err := s.repo.AddOrderOutcome(ctx, req.BuyerID.Int64(), req.SellerID.Int64(), req.Completed)
	if err != nil {
		return fmt.Errorf("add order outcome: %w", err)
	}
	return nil
}

// GetReputation reads the recomputed aggregate. An account nobody has rated has a reputation
// of zeroes rather than a 404 — that is what a new seller's profile shows.
func (s *Service) GetReputation(ctx context.Context, req trustapi.GetReputationRequest) (trustapi.Reputation, error) {
	if err := s.v.Struct(req); err != nil {
		return trustapi.Reputation{}, err
	}
	if _, err := s.accounts.GetPublicAccount(ctx, accountapi.GetPublicAccountRequest{ID: req.AccountID}); err != nil {
		return trustapi.Reputation{}, fmt.Errorf("read account: %w", err)
	}
	rep, err := s.repo.FindReputation(ctx, req.AccountID.Int64(), req.Role)
	if err != nil {
		return trustapi.Reputation{}, fmt.Errorf("find reputation: %w", err)
	}
	if rep.UpdatedAt.IsZero() {
		rep.UpdatedAt = time.Now()
	}
	return trustapi.Reputation{
		AccountID:           id.Of[id.Account](rep.AccountID),
		Role:                rep.Role,
		RatingAverage:       rep.AverageRating(),
		RatingCount:         rep.RatingCount,
		ReviewRatingAverage: rep.AverageReviewRating(),
		ReviewRatingCount:   rep.ReviewRatingCount,
		CompletedOrders:     rep.CompletedOrders,
		CancelledOrders:     rep.CancelledOrders,
		UpdatedAt:           rep.UpdatedAt,
	}, nil
}

func (s *Service) feedbackView(ctx context.Context, f domain.Feedback) (trustapi.Feedback, error) {
	rater, err := s.summary(ctx, f.RaterID)
	if err != nil {
		return trustapi.Feedback{}, err
	}
	return toAPIFeedback(f, rater), nil
}

func toAPIFeedback(f domain.Feedback, rater accountapi.AccountSummary) trustapi.Feedback {
	return trustapi.Feedback{
		ID:          id.Of[id.Feedback](f.ID),
		OrderID:     id.Of[id.Order](f.OrderID),
		Rater:       rater,
		RateeID:     id.Of[id.Account](f.RateeID),
		Direction:   f.Direction,
		Rating:      f.Rating,
		Comment:     f.Comment,
		PublishedAt: f.PublishedAt,
		CreatedAt:   f.CreatedAt,
	}
}
