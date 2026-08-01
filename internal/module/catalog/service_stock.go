package catalog

import (
	"context"
	"fmt"

	catalogapi "shopnexus/internal/module/catalog/api"
)

// The four stock movements. No role check and no listing load: order calls these on the
// checkout path, the guard is in the statement, and adding a read here would only widen the
// window in which the answer is already stale.

func (s *Service) ReserveStock(ctx context.Context, req catalogapi.StockMovementRequest) error {
	if err := s.repo.ReserveStock(ctx, req.VariantID.Int64(), req.Units); err != nil {
		return fmt.Errorf("reserve stock: %w", err)
	}
	return nil
}

func (s *Service) ReleaseStock(ctx context.Context, req catalogapi.StockMovementRequest) error {
	if err := s.repo.ReleaseStock(ctx, req.VariantID.Int64(), req.Units); err != nil {
		return fmt.Errorf("release stock: %w", err)
	}
	return nil
}

func (s *Service) CommitStock(ctx context.Context, req catalogapi.StockCommitRequest) error {
	if err := s.repo.CommitStock(ctx, req.VariantID.Int64(), req.Units, req.IdempotencyKey); err != nil {
		return fmt.Errorf("commit stock: %w", err)
	}
	return nil
}

func (s *Service) UncommitStock(ctx context.Context, req catalogapi.StockCommitRequest) error {
	if err := s.repo.UncommitStock(ctx, req.VariantID.Int64(), req.Units, req.IdempotencyKey); err != nil {
		return fmt.Errorf("uncommit stock: %w", err)
	}
	return nil
}

// SyncListingRating writes the review average trust recomputed. A listing that no longer
// exists is not an error: its reviews outlive it, and there is nothing left to cache.
func (s *Service) SyncListingRating(ctx context.Context, req catalogapi.SyncListingRatingRequest) error {
	if err := s.repo.SetCachedRating(ctx, req.ListingID.Int64(), req.Rating, req.Count); err != nil {
		return fmt.Errorf("set cached rating: %w", err)
	}
	return nil
}
