package account

import (
	"context"
	"fmt"

	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/validation"
)

// Follow is idempotent, and following yourself is refused: the edge would be a
// self-loop in the seller graph, and the CHECK on the table says so too.
func (s *Service) Follow(ctx context.Context, req accountapi.FollowRequest) error {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return err
	}
	if req.ActorID == req.TargetID {
		return domain.ErrSelfFollow
	}
	inserted, err := s.repo.InsertFollow(ctx, req.ActorID.Int64(), req.TargetID.Int64())
	if err != nil {
		return fmt.Errorf("insert follow: %w", err)
	}
	if inserted {
		s.notifyNewFollower(ctx, req.ActorID, req.TargetID)
	}
	return nil
}

// notifyNewFollower tells a seller somebody started following their shop.
//
// Called in-process rather than over the bus: both sides of a follow are accounts, so this is
// the module telling itself, and a topic would exist only to be consumed one line further down.
// Best-effort — the edge is already written, and a feed row is not worth failing a follow over.
func (s *Service) notifyNewFollower(ctx context.Context, followerID, followeeID id.ID[id.Account]) {
	// The follower's name is read here rather than left to the copybook: the row stores facts,
	// and a name is a fact this module already has a light read for.
	follower, err := s.repo.FindProfile(ctx, followerID.Int64())
	if err != nil {
		s.log.Error("read follower profile for notification", "account_id", followerID.Int64(), "err", err)
		return
	}
	_, err = s.CreateNotification(ctx, accountapi.CreateNotificationRequest{
		AccountID: followeeID,
		Kind:      string(domain.KindNewFollower),
		Payload: map[string]any{
			"follower_id":   followerID.String(),
			"follower_name": follower.Name,
		},
	})
	if err != nil {
		s.log.Error("notify new follower failed", "account_id", followeeID.Int64(), "err", err)
	}
}

// Unfollow is idempotent too, so a client that lost track of the state can always ask
// for the state it wants.
func (s *Service) Unfollow(ctx context.Context, req accountapi.UnfollowRequest) error {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return err
	}
	if err := s.repo.DeleteFollow(ctx, req.ActorID.Int64(), req.TargetID.Int64()); err != nil {
		return fmt.Errorf("delete follow: %w", err)
	}
	return nil
}

func (s *Service) ListFollowing(ctx context.Context, req accountapi.ListFollowingRequest) (accountapi.Page[accountapi.AccountSummary], error) {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return accountapi.Page[accountapi.AccountSummary]{}, err
	}
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
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return accountapi.Page[accountapi.AccountSummary]{}, err
	}
	if _, err := s.repo.Get(ctx, req.AccountID.Int64()); err != nil {
		return accountapi.Page[accountapi.AccountSummary]{}, fmt.Errorf("get account: %w", err)
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
