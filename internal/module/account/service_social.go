package account

import (
	"context"
	"fmt"

	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/domain"
)

// Follow is idempotent, and following yourself is refused: the edge would be a
// self-loop in the seller graph, and the CHECK on the table says so too.
func (s *Service) Follow(ctx context.Context, req accountapi.FollowRequest) error {
	if req.ActorID == req.TargetID {
		return domain.ErrSelfFollow
	}
	if err := s.repo.InsertFollow(ctx, req.ActorID.Int64(), req.TargetID.Int64()); err != nil {
		return fmt.Errorf("insert follow: %w", err)
	}
	return nil
}

// Unfollow is idempotent too, so a client that lost track of the state can always ask
// for the state it wants.
func (s *Service) Unfollow(ctx context.Context, req accountapi.UnfollowRequest) error {
	if err := s.repo.DeleteFollow(ctx, req.ActorID.Int64(), req.TargetID.Int64()); err != nil {
		return fmt.Errorf("delete follow: %w", err)
	}
	return nil
}

func (s *Service) ListFollowing(ctx context.Context, req accountapi.ListFollowingRequest) (accountapi.Page[accountapi.AccountSummary], error) {
	rows, total, err := s.repo.ListFollowing(ctx, req.ActorID.Int64(), offsetOf(req.Page, req.Limit), req.Limit)
	if err != nil {
		return accountapi.Page[accountapi.AccountSummary]{}, fmt.Errorf("list following: %w", err)
	}
	return accountapi.Page[accountapi.AccountSummary]{
		Data: s.summaries(ctx, rows),
		Meta: accountapi.PageInfo{Page: req.Page, Limit: req.Limit, TotalCount: &total},
	}, nil
}

// ListFollowers is public, so the account has to be looked up: an unknown seller is a
// 404, not an empty follower list, which a client cannot tell from a new seller.
func (s *Service) ListFollowers(ctx context.Context, req accountapi.ListFollowersRequest) (accountapi.Page[accountapi.AccountSummary], error) {
	if _, err := s.repo.FindAccountByID(ctx, req.AccountID.Int64()); err != nil {
		return accountapi.Page[accountapi.AccountSummary]{}, fmt.Errorf("find account by id: %w", err)
	}
	rows, total, err := s.repo.ListFollowers(ctx, req.AccountID.Int64(), offsetOf(req.Page, req.Limit), req.Limit)
	if err != nil {
		return accountapi.Page[accountapi.AccountSummary]{}, fmt.Errorf("list followers: %w", err)
	}
	return accountapi.Page[accountapi.AccountSummary]{
		Data: s.summaries(ctx, rows),
		Meta: accountapi.PageInfo{Page: req.Page, Limit: req.Limit, TotalCount: &total},
	}, nil
}

// offsetOf turns the 1-based page the API speaks into the offset SQL wants.
func offsetOf(page, limit int) int { return (page - 1) * limit }
