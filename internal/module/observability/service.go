package observability

import (
	"context"
	"fmt"

	"github.com/go-playground/validator/v10"

	observabilityapi "shopnexus/internal/module/observability/api"
	"shopnexus/internal/module/observability/port"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/validation"
)

// Service implements observabilityapi.Service. Unlike every other module's, it orchestrates
// nothing beyond one repository read — there is no domain to hold an invariant, because a
// popularity score has none this layer enforces; the adapter's UPDATE-then-INSERT is the only
// rule the number obeys.
type Service struct {
	repo port.Repository
	v    *validator.Validate
}

func NewService(repo port.Repository, v *validator.Validate) *Service {
	return &Service{repo: repo, v: v}
}

var _ observabilityapi.Service = (*Service)(nil)

func (s *Service) TopPopular(ctx context.Context, req observabilityapi.TopPopularRequest) ([]id.ID[id.Listing], error) {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return nil, err
	}
	rows, err := s.repo.TopPopularListings(ctx, req.Offset, req.Limit)
	if err != nil {
		return nil, fmt.Errorf("list top popular: %w", err)
	}
	out := make([]id.ID[id.Listing], len(rows))
	for i, listingID := range rows {
		out[i] = id.Of[id.Listing](listingID)
	}
	return out, nil
}
