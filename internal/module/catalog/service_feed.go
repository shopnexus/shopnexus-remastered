package catalog

import (
	"context"
	"fmt"

	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/module/catalog/port"
	"shopnexus/internal/shared/errx"
)

// ListListings answers the feed, the search, the wishlist page and the id lookup. The
// parameters narrow one query; the combinations that have no answer are refused here rather
// than resolved by precedence, so a client never gets a different list than it asked for.
func (s *Service) ListListings(ctx context.Context, req catalogapi.ListListingsRequest) (catalogapi.ListingPage, error) {
	filter, err := s.feedFilter(ctx, req)
	if err != nil {
		return catalogapi.ListingPage{}, err
	}
	rows, total, err := s.repo.ListListings(ctx, filter)
	if err != nil {
		return catalogapi.ListingPage{}, fmt.Errorf("list listings: %w", err)
	}
	cards, err := s.cards(ctx, rows)
	if err != nil {
		return catalogapi.ListingPage{}, err
	}
	// A card says whether the viewer saved it, in one query for the page rather than one each.
	if req.ViewerID != 0 {
		saved, err := s.repo.FavoritedAmong(ctx, req.ViewerID.Int64(), listingKeys(rows))
		if err != nil {
			return catalogapi.ListingPage{}, fmt.Errorf("read favorited: %w", err)
		}
		for i := range cards {
			cards[i].Favorited = saved[cards[i].ID.Int64()]
		}
	}
	return catalogapi.ListingPage{
		Data: cards,
		Meta: catalogapi.PageInfo{Page: req.Page, Limit: req.Limit, TotalCount: &total},
	}, nil
}

// feedFilter validates the combination and resolves what the adapter cannot: the search probe,
// and the interest vectors a recommended feed ranks against.
func (s *Service) feedFilter(ctx context.Context, req catalogapi.ListListingsRequest) (port.ListingFilter, error) {
	// The three filters that are about the caller need to know who that is. An empty page would
	// answer a different question than the one asked.
	if (req.Mine || req.Favorited || req.Sort == port.SortRecommended) && req.ViewerID == 0 {
		return port.ListingFilter{}, domain.ErrAuthenticationRequired
	}
	if req.Status != "" && !req.Mine {
		return port.ListingFilter{}, errx.NewValidationError("invalid field: status", errx.Field{
			Field: "status", Rule: "excluded_without",
			Message: "only honoured with mine=true: a seller may see what is not public, nobody else",
		})
	}
	if req.Sort == port.SortRelevance && req.Query == "" {
		return port.ListingFilter{}, errx.NewValidationError("invalid field: sort", errx.Field{
			Field: "sort", Rule: "excluded_without", Message: "relevance needs a query to be relevant to",
		})
	}
	if req.Sort == port.SortRecommended && (req.Favorited || req.Mine) {
		return port.ListingFilter{}, errx.NewValidationError("invalid field: sort", errx.Field{
			Field: "sort", Rule: "excluded_with",
			Message: "a personalised ranking of a set the caller already chose ranks nothing",
		})
	}

	filter := port.ListingFilter{
		Query:     req.Query,
		Mode:      req.Mode,
		ViewerID:  req.ViewerID.Int64(),
		Mine:      req.Mine,
		Favorited: req.Favorited,
		Status:    domain.Status(req.Status),
		Tag:       req.Tag,
		Condition: domain.Condition(req.Condition),
		Sort:      sortOf(req),
		Offset:    offsetOf(req.Page, req.Limit),
		Limit:     req.Limit,
	}
	for _, listingID := range req.IDs {
		filter.IDs = append(filter.IDs, listingID.Int64())
	}
	for _, variantID := range req.Variants {
		filter.VariantIDs = append(filter.VariantIDs, variantID.Int64())
	}
	if req.CategoryID != nil {
		filter.CategoryID = req.CategoryID.Int64()
	}
	if req.SellerID != nil {
		filter.SellerID = req.SellerID.Int64()
	}
	if req.MinPrice != nil {
		filter.MinPrice = *req.MinPrice
	}
	if req.MaxPrice != nil {
		filter.MaxPrice = *req.MaxPrice
	}

	probe, err := s.probeFor(ctx, req)
	if err != nil {
		return port.ListingFilter{}, err
	}
	filter.Probe = probe
	// A recommended feed with nothing computed for the account yet is the newest feed, not an
	// empty one — the contract says it falls back.
	if filter.Sort == port.SortRecommended && filter.Probe == nil {
		filter.Sort = port.SortNewest
	}
	return filter, nil
}

// probeFor resolves the dense vector a ranking runs against: the caller's interests for a
// recommended feed, or the query's embedding for a semantic or hybrid search.
//
// The query embedding needs the same model that wrote listing_embedding, and nothing in this
// module can call it yet — so a semantic search degrades to lexical rather than answering
// nothing, and a client still gets results while that seam is missing.
func (s *Service) probeFor(ctx context.Context, req catalogapi.ListListingsRequest) (port.Vector, error) {
	if req.Sort == port.SortRecommended {
		vectors, err := s.repo.InterestVectors(ctx, req.ViewerID.Int64())
		if err != nil {
			return nil, fmt.Errorf("read interest vectors: %w", err)
		}
		if len(vectors) == 0 {
			return nil, nil
		}
		return vectors[0], nil
	}
	return nil, nil
}

// sortOf applies the two defaults the contract names: relevance when a query was given, newest
// otherwise.
func sortOf(req catalogapi.ListListingsRequest) string {
	if req.Sort != "" {
		return req.Sort
	}
	if req.Query != "" {
		return port.SortRelevance
	}
	return port.SortNewest
}

func listingKeys(rows []port.ListingSummary) []int64 {
	out := make([]int64, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.ID)
	}
	return out
}

// AddFavorite saves a listing to the caller's wishlist. Idempotent, and it refuses a listing
// nobody can see: a wishlist of unreadable ids is a page that renders nothing.
func (s *Service) AddFavorite(ctx context.Context, req catalogapi.FavoriteRequest) error {
	l, err := s.repo.GetListing(ctx, req.ID.Int64())
	if err != nil {
		return fmt.Errorf("get listing: %w", err)
	}
	if l.Status == domain.StatusDraft || l.Status == domain.StatusPending {
		if l.SellerID != req.ActorID.Int64() {
			return domain.ErrListingNotFound
		}
	}
	if err := s.repo.AddFavorite(ctx, req.ActorID.Int64(), req.ID.Int64()); err != nil {
		return fmt.Errorf("add favorite: %w", err)
	}
	return nil
}

// RemoveFavorite takes it off the wishlist. It does not check the listing exists: unsaving
// something already gone is the state the caller asked for, and a 404 would leave them unable
// to clean up a wishlist whose listing was deleted.
func (s *Service) RemoveFavorite(ctx context.Context, req catalogapi.FavoriteRequest) error {
	if err := s.repo.RemoveFavorite(ctx, req.ActorID.Int64(), req.ID.Int64()); err != nil {
		return fmt.Errorf("remove favorite: %w", err)
	}
	return nil
}
