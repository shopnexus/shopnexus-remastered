package catalog

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	accountapi "shopnexus/internal/module/account/api"
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
	meta := catalogapi.PageInfo{Page: req.Page, Limit: req.Limit}
	// A ranked query — relevance or recommended — visits only its top-K, the way the
	// dictionary's `near` ranking does; the count behind it is not a stable, seekable
	// total and would read as one.
	if filter.Sort != port.SortRelevance && filter.Sort != port.SortRecommended {
		meta.TotalCount = &total
	}
	return catalogapi.ListingPage{Data: cards, Meta: meta}, nil
}

// feedFilter validates the combination and resolves what the adapter cannot: the search probe,
// and the interest vectors a recommended feed ranks against.
// browsePosition is where the buyer is browsing from: the coordinates the device sent, or the
// coordinates of one of their own saved addresses. Nil when they named neither, and an error when
// the address they named has never been geocoded — a distance nobody can compute is not an empty
// result.
func (s *Service) browsePosition(ctx context.Context, req catalogapi.ListListingsRequest) (*port.Point, error) {
	if req.Latitude != nil && req.Longitude != nil {
		return &port.Point{Latitude: *req.Latitude, Longitude: *req.Longitude}, nil
	}
	if req.NearContactID == nil {
		return nil, nil
	}
	if req.ViewerID == 0 {
		return nil, domain.ErrAuthenticationRequired
	}
	contact, err := s.accounts.GetContact(ctx, accountapi.GetContactRequest{
		ActorID: req.ViewerID, ID: *req.NearContactID,
	})
	if err != nil {
		return nil, fmt.Errorf("read contact: %w", err)
	}
	if contact.Latitude == nil || contact.Longitude == nil {
		return nil, domain.ErrAddressNotGeocoded
	}
	return &port.Point{Latitude: *contact.Latitude, Longitude: *contact.Longitude}, nil
}

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

	if (req.Latitude == nil) != (req.Longitude == nil) {
		return port.ListingFilter{}, errx.NewValidationError("invalid field: lat", errx.Field{
			Field: "lat", Rule: "required_with",
			Message: "a position needs both lat and lon",
		})
	}
	if req.NearContactID != nil && req.Latitude != nil {
		return port.ListingFilter{}, errx.NewValidationError("invalid field: near_contact_id", errx.Field{
			Field: "near_contact_id", Rule: "excluded_with",
			Message: "name a position or an address of yours, not both",
		})
	}

	filter := port.ListingFilter{
		Query:     req.Query,
		Mode:      modeOf(req),
		ViewerID:  req.ViewerID.Int64(),
		Mine:      req.Mine,
		Favorited: req.Favorited,
		Status:    domain.Status(req.Status),
		Tag:       req.Tag,
		Condition: domain.Condition(req.Condition),

		ProvinceCode: req.ProvinceCode,
		DistrictCode: req.DistrictCode,
		WardCode:     req.WardCode,
		Sort:         sortOf(req),
		Offset:       offsetOf(req.Page, req.Limit),
		Limit:        req.Limit,
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
	filter.MaxPrice = req.MaxPrice
	if req.RadiusKM != nil {
		filter.RadiusKM = *req.RadiusKM
	}
	near, err := s.browsePosition(ctx, req)
	if err != nil {
		return port.ListingFilter{}, err
	}
	filter.Near = near
	// A radius or a distance sort with nowhere to measure from would silently answer a different
	// question — every listing, in creation order — so it is refused instead.
	if near == nil && (filter.RadiusKM > 0 || filter.Sort == port.SortDistance) {
		return port.ListingFilter{}, errx.NewValidationError("invalid field: lat", errx.Field{
			Field: "lat", Rule: "required",
			Message: "a distance needs a position: send lat and lon, or near_contact_id",
		})
	}

	probe, fromQuery, err := s.probeFor(ctx, req)
	if err != nil {
		return port.ListingFilter{}, err
	}
	filter.Probe = probe
	filter.ProbeFromQuery = fromQuery
	// A recommended feed with nothing computed for the account yet is the newest feed, not an
	// empty one — the contract says it falls back.
	if filter.Sort == port.SortRecommended && filter.Probe == nil {
		filter.Sort = port.SortNewest
	}
	return filter, nil
}

// probeFor resolves the dense vector a ranking runs against: the caller's interests for a
// recommended feed, or the query's embedding for a semantic or hybrid search. The second
// return says which one it is — a recommended feed's probe has nothing to do with Query,
// so the adapter must not treat it as satisfying a lexical filter the caller asked for.
//
// Embedding the query is **best-effort**: a model that is down, slow or not deployed at all
// leaves the probe nil, and the search runs lexically instead of failing. That is the same
// bargain the rest of the pipeline makes — a listing with no embedding is still findable by
// name — and it is what keeps the model off the critical path of the busiest route.
func (s *Service) probeFor(ctx context.Context, req catalogapi.ListListingsRequest) (port.Vector, bool, error) {
	if req.Sort == port.SortRecommended {
		vectors, err := s.repo.InterestVectors(ctx, req.ViewerID.Int64())
		if err != nil {
			return nil, false, fmt.Errorf("read interest vectors: %w", err)
		}
		if len(vectors) == 0 {
			return nil, false, nil
		}
		return vectors[0], false, nil
	}
	if req.Query == "" || modeOf(req) == port.ModeLexical {
		return nil, false, nil
	}
	vector, err := s.queryVector(ctx, req.Query)
	if err != nil {
		s.log.Warn("embed search query, falling back to lexical", "err", err)
		return nil, false, nil
	}
	return vector, true, nil
}

// queryVectorTTL is how long a query's embedding is kept. Long, because the answer only
// changes when the model does: the same words always produce the same vector, and what the
// cache is protecting against is a popular query paying for an inference every time it is
// typed. A domain constant rather than config — nothing about a deployment changes it.
const queryVectorTTL = 24 * time.Hour

// queryVector embeds one search query, through the cache.
//
// The key carries the model's name and the width it answered in, so a deployment that changes
// either reads none of the old entries instead of ranking today's listings against yesterday's
// model — two vectors from different models are not comparable, and the failure would look
// like search quietly getting worse rather than like a cache to clear. A cache that is down is
// not an error: it costs an inference, not an answer.
func (s *Service) queryVector(ctx context.Context, query string) (port.Vector, error) {
	normalized := strings.ToLower(strings.Join(strings.Fields(query), " "))
	sum := sha256.Sum256([]byte(normalized))
	key := fmt.Sprintf("search-vec:%s:%d:%x", s.vectors.Name(), len(normalized), sum)

	var cached port.Vector
	if err := s.cache.Get(ctx, key, &cached); err == nil && len(cached) > 0 {
		return cached, nil
	}

	vectors, err := s.vectors.Embed(ctx, []string{normalized})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(vectors) != 1 || len(vectors[0].Dense) == 0 {
		return nil, fmt.Errorf("embed query: model answered %d vectors", len(vectors))
	}
	vector := port.Vector(vectors[0].Dense)
	if err := s.cache.Set(ctx, key, vector, queryVectorTTL); err != nil {
		s.log.Warn("cache search query vector", "err", err)
	}
	return vector, nil
}

// modeOf applies the default the contract names: hybrid, which is what a model producing both a
// dense and a lexical half is for. Only meaningful with a query, so it is left empty without one
// — an empty mode is what tells scoreExpr a feed is not a search.
func modeOf(req catalogapi.ListListingsRequest) string {
	if req.Mode != "" || req.Query == "" {
		return req.Mode
	}
	return port.ModeHybrid
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
