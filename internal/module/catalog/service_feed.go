package catalog

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"shopnexus/internal/infra/cache"
	accountapi "shopnexus/internal/module/account/api"
	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/module/catalog/port"
	observabilityapi "shopnexus/internal/module/observability/api"
	"shopnexus/internal/shared/errx"
)

// ListListings answers the feed, the search, the wishlist page and the id lookup. The
// parameters narrow one query; the combinations that have no answer are refused here rather
// than resolved by precedence, so a client never gets a different list than it asked for.
func (s *Service) ListListings(ctx context.Context, req catalogapi.ListListingsRequest) (catalogapi.ListingPage, error) {
	filter, answer, err := s.feedFilter(ctx, req)
	if err != nil {
		return catalogapi.ListingPage{}, err
	}
	rows, total, err := s.listingRows(ctx, filter)
	if err != nil {
		return catalogapi.ListingPage{}, fmt.Errorf("list listings: %w", err)
	}
	cards, err := s.listingCards(ctx, rows, req.ViewerID.Int64())
	if err != nil {
		return catalogapi.ListingPage{}, err
	}
	meta := catalogapi.PageInfo{Page: req.Page, Limit: req.Limit}
	// A ranked query — relevance, recommended or trending — visits only its top-K, the way the
	// dictionary's `near` ranking does; the count behind it is not a stable, seekable
	// total and would read as one.
	if filter.Sort != port.SortRelevance && filter.Sort != port.SortRecommended && filter.Sort != port.SortTrending {
		meta.TotalCount = &total
	}
	page := catalogapi.ListingPage{
		Data: cards, Meta: meta, Understood: answer.understood, Probes: answer.probes,
	}
	// The contract says an array. json/v2 already marshals a nil slice as `[]`, so this is a
	// second line and not the thing holding the shape: it keeps the field an array for any
	// encoder that reaches this struct.
	if page.Probes == nil {
		page.Probes = []string{}
	}
	return page, nil
}

// listingRows runs whichever of the three retrieval shapes the filter asks for. The browse and
// the search are one statement in the adapter; a personalised draw and the trending top list are
// their own, and which one a filter means is decided here rather than at each caller — the home
// page's shelves ask the same question the feed does.
//
// Total is zero for the two that are not seekable, which is also why ListListings only publishes
// it for the others.
func (s *Service) listingRows(ctx context.Context, filter port.ListingFilter) ([]port.ListingSummary, int64, error) {
	switch {
	case filter.Sort == port.SortRecommended && len(filter.Interests) > 0:
		rows, err := s.recommendedRows(ctx, filter)
		return rows, 0, err
	case filter.Sort == port.SortTrending:
		rows, err := s.trendingRows(ctx, filter)
		return rows, 0, err
	default:
		return s.repo.ListListings(ctx, filter)
	}
}

// listingCards is the card projection plus the one thing a card can only say about the caller:
// whether they saved it. One query for the whole page rather than one per row, and skipped
// entirely for an anonymous read, which has nothing saved.
func (s *Service) listingCards(ctx context.Context, rows []port.ListingSummary, viewerID int64) ([]catalogapi.Listing, error) {
	cards, err := s.cards(ctx, rows)
	if err != nil {
		return nil, err
	}
	if viewerID == 0 {
		return cards, nil
	}
	saved, err := s.repo.FavoritedAmong(ctx, viewerID, listingKeys(rows))
	if err != nil {
		return nil, fmt.Errorf("read favorited: %w", err)
	}
	for i := range cards {
		cards[i].Favorited = saved[cards[i].ID.Int64()]
	}
	return cards, nil
}

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

// searchAnswer is what the understanding stage has to tell the shopper: the sentence and the
// phrases actually searched. Carried out of feedFilter rather than recomputed, because the model
// call that produced it happens once — and out of it rather than onto the Service, since a field
// there would be read by whichever request arrived next.
type searchAnswer struct {
	understood string
	probes     []string
}

// feedFilter validates the combination and resolves what the adapter cannot: where the buyer
// is, the search terms, and the interests a personalised feed ranks against.
func (s *Service) feedFilter(ctx context.Context, req catalogapi.ListListingsRequest) (port.ListingFilter, searchAnswer, error) {
	// The three filters that are about the caller need to know who that is. An empty page would
	// answer a different question than the one asked.
	if (req.Mine || req.Favorited || req.Sort == port.SortRecommended) && req.ViewerID == 0 {
		return port.ListingFilter{}, searchAnswer{}, domain.ErrAuthenticationRequired
	}
	if req.Status != "" && !req.Mine {
		return port.ListingFilter{}, searchAnswer{}, errx.NewValidationError("invalid field: status", errx.Field{
			Field: "status", Rule: "excluded_without",
			Message: "only honoured with mine=true: a seller may see what is not public, nobody else",
		})
	}
	// `similar_to` is the other thing relevance can be relevant to.
	if req.Sort == port.SortRelevance && req.Query == "" && req.SimilarTo == nil {
		return port.ListingFilter{}, searchAnswer{}, errx.NewValidationError("invalid field: sort", errx.Field{
			Field: "sort", Rule: "excluded_without", Message: "relevance needs a query to be relevant to",
		})
	}
	if req.Sort == port.SortRecommended && (req.Favorited || req.Mine) {
		return port.ListingFilter{}, searchAnswer{}, errx.NewValidationError("invalid field: sort", errx.Field{
			Field: "sort", Rule: "excluded_with",
			Message: "a personalised ranking of a set the caller already chose ranks nothing",
		})
	}
	if req.SimilarTo != nil {
		// Two rankings in one request answer neither: `q` compiles a query into probes and
		// `similar_to` *is* the probe, and a personalised feed ranks against the account rather
		// than against a listing. A set the caller already chose has nothing to rank either.
		switch {
		case req.Query != "":
			return port.ListingFilter{}, searchAnswer{}, errx.NewValidationError("invalid field: similar_to", errx.Field{
				Field: "similar_to", Rule: "excluded_with",
				Message: "rank by a query or by a listing, not both",
			})
		case req.Sort != "" && req.Sort != port.SortRelevance:
			return port.ListingFilter{}, searchAnswer{}, errx.NewValidationError("invalid field: sort", errx.Field{
				Field: "sort", Rule: "excluded_with",
				Message: "similar_to is itself the ranking; leave sort out or ask for relevance",
			})
		case req.Mine || req.Favorited:
			return port.ListingFilter{}, searchAnswer{}, errx.NewValidationError("invalid field: similar_to", errx.Field{
				Field: "similar_to", Rule: "excluded_with",
				Message: "a set the caller already chose has nothing to rank against a listing",
			})
		}
	}
	if req.Sort == port.SortTrending && trendingWantsFilteredBrowse(req) {
		return port.ListingFilter{}, searchAnswer{}, errx.NewValidationError("invalid field: sort", errx.Field{
			Field: "sort", Rule: "excluded_with",
			Message: "trending is the platform's whole top list, unfiltered — narrow the browse with another sort",
		})
	}

	if (req.Latitude == nil) != (req.Longitude == nil) {
		return port.ListingFilter{}, searchAnswer{}, errx.NewValidationError("invalid field: lat", errx.Field{
			Field: "lat", Rule: "required_with",
			Message: "a position needs both lat and lon",
		})
	}
	if req.NearContactID != nil && req.Latitude != nil {
		return port.ListingFilter{}, searchAnswer{}, errx.NewValidationError("invalid field: near_contact_id", errx.Field{
			Field: "near_contact_id", Rule: "excluded_with",
			Message: "name a position or an address of yours, not both",
		})
	}

	filter := port.ListingFilter{
		Query:     req.Query,
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
		return port.ListingFilter{}, searchAnswer{}, err
	}
	filter.Near = near
	// A radius or a distance sort with nowhere to measure from would silently answer a different
	// question — every listing, in creation order — so it is refused instead.
	if near == nil && (filter.RadiusKM > 0 || filter.Sort == port.SortDistance) {
		return port.ListingFilter{}, searchAnswer{}, errx.NewValidationError("invalid field: lat", errx.Field{
			Field: "lat", Rule: "required",
			Message: "a distance needs a position: send lat and lon, or near_contact_id",
		})
	}

	if filter.Sort == port.SortRecommended {
		interests, err := s.repo.Interests(ctx, req.ViewerID.Int64())
		if err != nil {
			return port.ListingFilter{}, searchAnswer{}, fmt.Errorf("read interests: %w", err)
		}
		filter.Interests = interests
		filter.Seed = feedSeed(req.Seed, req.ViewerID.Int64(), time.Now())
		// A recommended feed with nothing computed for the account yet falls back rather than
		// answering empty — to trending, the platform's own best guess, when the rest of the
		// request is a bare browse (the same test a direct sort=trending is refused without
		// meeting, trendingWantsFilteredBrowse); to newest otherwise, which is what still lets
		// a query or a category narrow the fallback below.
		if len(interests) == 0 {
			if trendingWantsFilteredBrowse(req) {
				filter.Sort = port.SortNewest
			} else {
				filter.Sort = port.SortTrending
			}
		}
	}
	// "More like this one" is one probe and no model call: the listing's own stored embedding
	// is the query. The anchor is excluded because its own vector is its own best match, and a
	// listing the embedding pass has not reached yet degrades to the newest of its category —
	// still a rail about the same kind of thing, rather than an empty one.
	if req.SimilarTo != nil {
		filter.Sort = port.SortRelevance
		filter.ExcludeIDs = append(filter.ExcludeIDs, req.SimilarTo.Int64())
		probe, err := s.repo.ListingProbe(ctx, req.SimilarTo.Int64())
		switch {
		case errors.Is(err, domain.ErrListingNotEmbedded):
			filter.Sort = port.SortNewest
		case err != nil:
			return port.ListingFilter{}, searchAnswer{}, fmt.Errorf("read listing probe: %w", err)
		default:
			filter.Terms = []port.Term{{Weight: 1, Probe: &probe}}
		}
		return filter, searchAnswer{}, nil
	}
	// Every search reaches the adapter as terms — the understood signals, then the shopper's own
	// words. A personalised feed is left out: it ranks against the account's interests, so an
	// understood query would have nothing to rank (`q` there is a filter on the name, see
	// recommendedWhere) and the model call would be paid for and thrown away.
	var answer searchAnswer
	if filter.Sort != port.SortRecommended && req.Query != "" {
		terms, said, err := s.searchTerms(ctx, req, filter)
		if err != nil {
			return port.ListingFilter{}, searchAnswer{}, err
		}
		filter.Terms = terms
		answer = said
	}
	return filter, answer, nil
}

// trendingWantsFilteredBrowse is true when the request narrows the browse some way trending
// cannot honour: listing_popularity ranks the whole catalogue, with nothing to join it against
// a category, a price range, a search or a position. Mine and Favorited are checked by the
// caller alongside recommended's own refusal, for the same reason.
func trendingWantsFilteredBrowse(req catalogapi.ListListingsRequest) bool {
	return req.Mine || req.Favorited || req.Query != "" ||
		req.CategoryID != nil || req.Tag != "" || req.SellerID != nil || req.Condition != "" ||
		req.MinPrice != nil || req.MaxPrice != nil ||
		req.ProvinceCode != "" || req.DistrictCode != "" || req.WardCode != "" ||
		req.Latitude != nil || req.NearContactID != nil
}

// feedSeed decides which of the many good orderings of a personalised feed this request gets.
//
// The caller's own seed is honoured because only they know where one browsing run ends: the
// ordering has to hold still while they page down, and it has to change when they come back.
// A client that names none still moves, on a clock rather than on nothing — the alternative
// is a feed that is identical for ever, which is the complaint that put this here. Scoped to
// the account so two people never walk through the catalogue in step.
func feedSeed(sent string, viewerID int64, now time.Time) string {
	if sent != "" {
		return sent
	}
	return fmt.Sprintf("%d:%d", viewerID, now.Unix()/int64(domain.SeedRotation.Seconds()))
}

// feedCacheBatch is how many ids one draw of the personalised feed materialises at once —
// several pages' worth, so a normal scroll never pays for a redraw mid-page.
const feedCacheBatch = 60

// feedCacheTTL is how long a materialised batch is trusted. Long enough to cover one
// browsing session's paging, short enough that a seed reused later still finds a fresh draw —
// and short enough that a demo account's new favorite or view, recomputed by the next interest
// sweep, shows up in the same seed's next page rather than behind a stale batch.
const feedCacheTTL = 2 * time.Minute

// feedCacheKey scopes a materialised batch to the run that drew it: the account, the seed and
// every filter that narrowed the draw. The filter half is not optional — a personalised feed
// still takes a category, a price range, an area and a name query, so a key of (account, seed)
// alone served a filtered browse from whatever the previous, differently filtered browse of the
// same run had cached, which is a page answering a question nobody asked.
//
// Left out on purpose: Offset and Limit, which are the paging this cache exists to serve;
// Interests, whose staleness bound is feedCacheTTL rather than a key; Terms — the only filter
// that ever carries one is the personalised feed, which never carries a query, so hashing a
// slice of 1024-float probes would cost a serialisation per page for nothing; and SkipTotal,
// because what this cache holds is a list of ids and nothing else. Two callers differing only
// in whether they want a count want the same ids, so keying on it would halve the hit rate and
// buy nothing.
//
// A new field on port.ListingFilter belongs here too — TestFeedCacheKey_CoversEveryFilterField
// is what says so, since nothing else would.
func feedCacheKey(f port.ListingFilter) string {
	maxPrice := "-"
	if f.MaxPrice != nil {
		maxPrice = strconv.FormatInt(*f.MaxPrice, 10)
	}
	near := "-"
	if f.Near != nil {
		near = fmt.Sprintf("%g,%g", f.Near.Latitude, f.Near.Longitude)
	}
	h := sha256.New()
	// ExcludeIDs is in the key because the home page's shelves are the same personalised draw
	// with a different exclusion each time: shelf three asks for the same interests as shelf two
	// minus everything shelf two showed. Left out of the key, the second shelf would be served
	// the first one's cached batch and the page would repeat itself under two headings.
	// Discarded rather than checked: `hash.Hash` documents that Write never returns an error,
	// so there is nothing here a caller could do or a test could reach.
	// MatchedOnly is in the key and SkipTotal is not, which looks inconsistent and is not: this
	// cache holds ids, and dropping the fresh source changes which ids the draw produces, while
	// wanting a count changes nothing about them.
	_, _ = fmt.Fprintf(h, "%v|%v|%v|%s|%t|%t|%s|%d|%s|%d|%s|%d|%s|%s|%s|%s|%s|%g|%s|%t",
		f.IDs, f.VariantIDs, f.ExcludeIDs, f.Query, f.Mine, f.Favorited, f.Status,
		f.CategoryID, f.Tag, f.SellerID, f.Condition, f.MinPrice, maxPrice,
		f.ProvinceCode, f.DistrictCode, f.WardCode, near, f.RadiusKM, f.Sort, f.MatchedOnly)
	return fmt.Sprintf("feed:%d:%s:%x", f.ViewerID, f.Seed, h.Sum(nil)[:8])
}

// recommendedRows answers one page of the personalised feed. The first ask for a
// (viewer, seed) draws feedCacheBatch ids and caches the order; every later page slices that
// order and re-reads only those ids, fresh, instead of redoing the weighted draw. A page
// past what is cached draws again for more of the same run — the ordering is a pure function
// of the seed, so asking for more is asking the same question with a bigger limit.
func (s *Service) recommendedRows(ctx context.Context, filter port.ListingFilter) ([]port.ListingSummary, error) {
	key := feedCacheKey(filter)
	want := filter.Offset + filter.Limit

	var ids []int64
	if err := s.cache.Get(ctx, key, &ids); err != nil && !errors.Is(err, cache.ErrCacheMiss) {
		s.log.Warn("read feed cache", "account_id", filter.ViewerID, "err", err)
	}

	if len(ids) < want {
		draw := filter
		draw.Offset, draw.Limit = 0, max(want, feedCacheBatch)
		rows, _, err := s.repo.ListListings(ctx, draw)
		if err != nil {
			return nil, fmt.Errorf("draw recommended feed: %w", err)
		}
		if err := s.cache.Set(ctx, key, listingKeys(rows), feedCacheTTL); err != nil {
			s.log.Warn("cache recommended feed", "account_id", filter.ViewerID, "err", err)
		}
		return rows[min(filter.Offset, len(rows)):min(want, len(rows))], nil
	}

	page := ids[filter.Offset:min(want, len(ids))]
	rows, err := s.repo.ListListingsByIDs(ctx, page)
	if err != nil {
		return nil, fmt.Errorf("hydrate recommended feed: %w", err)
	}
	return reorderCards(rows, page), nil
}

// trendingRows answers the platform-wide top list: observability's listing_popularity, read
// back and hydrated into cards. No cache of its own, unlike recommendedRows — this is one
// index scan over a small aggregate table, the same shape and cost every viewer shares, so
// there is nothing a per-account materialisation would save.
//
// Reading it is best-effort: observability sits off this route's critical path, so a store it
// cannot reach degrades the page to newest rather than failing the request. The same backfill
// covers the ordinary case of a thin ranking — a young platform with fewer popular listings
// than one page wants.
func (s *Service) trendingRows(ctx context.Context, filter port.ListingFilter) ([]port.ListingSummary, error) {
	want := filter.Offset + filter.Limit

	// The exclusion has to be applied here rather than left to the adapter: this ranking comes
	// from another module's table and is hydrated by id, and ListListingsByIDs answers the ids it
	// is given whatever else the filter says. Asking for more than `want` is what keeps the row
	// full after the drop — the home page's shelves each exclude what the shelves above them
	// already showed, and a trending row that ignored that would repeat them.
	excluded := make(map[int64]bool, len(filter.ExcludeIDs))
	for _, id := range filter.ExcludeIDs {
		excluded[id] = true
	}

	var popular []int64
	ids, err := s.popularity.TopPopular(ctx, observabilityapi.TopPopularRequest{
		Limit: want + len(excluded),
	})
	if err != nil {
		s.log.Warn("read top popular, falling back to newest", "err", err)
	} else {
		popular = make([]int64, 0, len(ids))
		for _, listingID := range ids {
			if !excluded[listingID.Int64()] {
				popular = append(popular, listingID.Int64())
			}
		}
	}

	var rows []port.ListingSummary
	if len(popular) > 0 {
		hydrated, err := s.repo.ListListingsByIDs(ctx, popular)
		if err != nil {
			return nil, fmt.Errorf("hydrate trending feed: %w", err)
		}
		rows = reorderCards(hydrated, popular)
	}
	if len(rows) >= want {
		return rows[filter.Offset:want], nil
	}

	seen := make(map[int64]bool, len(rows))
	for _, r := range rows {
		seen[r.ID] = true
	}
	backfill := filter
	backfill.Sort, backfill.Offset, backfill.Limit = port.SortNewest, 0, want+len(seen)
	newest, _, err := s.repo.ListListings(ctx, backfill)
	if err != nil {
		return nil, fmt.Errorf("backfill trending feed: %w", err)
	}
	for _, r := range newest {
		if len(rows) >= want {
			break
		}
		if !seen[r.ID] {
			rows = append(rows, r)
			seen[r.ID] = true
		}
	}
	if filter.Offset >= len(rows) {
		return nil, nil
	}
	return rows[filter.Offset:min(want, len(rows))], nil
}

// reorderCards puts a hydration read back in the order ids named, dropping any that did not
// come back — delisted since the draw that named them.
func reorderCards(rows []port.ListingSummary, ids []int64) []port.ListingSummary {
	byID := make(map[int64]port.ListingSummary, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}
	out := make([]port.ListingSummary, 0, len(ids))
	for _, id := range ids {
		if r, ok := byID[id]; ok {
			out = append(out, r)
		}
	}
	return out
}

// queryVectorTTL is how long a query's embedding is kept. Long, because the answer only
// changes when the model does: the same words always produce the same vector, and what the
// cache is protecting against is a popular query paying for an inference every time it is
// typed. A domain constant rather than config — nothing about a deployment changes it.
const queryVectorTTL = 24 * time.Hour

// queryProbe embeds one text into both halves, through the cache.
//
// The key carries the model's name, so a deployment that changes it reads none of the old entries
// instead of ranking today's listings against yesterday's model; the length beside the digest is
// only there to keep two different texts from ever sharing a key. The prefix is `search-probe`
// rather than `search-vec`: an entry written before the sparse half was kept has no sparse half,
// and reading one back would silently drop a leg.
func (s *Service) queryProbe(ctx context.Context, text string) (port.Probe, error) {
	normalized := strings.ToLower(strings.Join(strings.Fields(text), " "))
	sum := sha256.Sum256([]byte(normalized))
	key := fmt.Sprintf("search-probe:%s:%d:%x", s.vectors.Name(), len(normalized), sum)

	var cached port.Probe
	if err := s.cache.Get(ctx, key, &cached); err == nil && len(cached.Dense) > 0 {
		return cached, nil
	}

	vectors, err := s.vectors.Embed(ctx, []string{normalized})
	if err != nil {
		return port.Probe{}, fmt.Errorf("embed query: %w", err)
	}
	if len(vectors) != 1 || len(vectors[0].Dense) == 0 {
		return port.Probe{}, fmt.Errorf("embed query: model answered %d vectors", len(vectors))
	}
	probe := port.Probe{Dense: port.Vector(vectors[0].Dense), Sparse: vectors[0].Sparse}
	if err := s.cache.Set(ctx, key, probe, queryVectorTTL); err != nil {
		s.log.Warn("cache search query probe", "err", err)
	}
	return probe, nil
}

// queryProbes resolves a whole list of texts, each through queryProbe's own cache lookup. It
// does not batch the model call: batching across a partial cache hit needs an index map to put
// the misses back in place, and with at most a handful of probes per query the simpler shape is
// the right one until a measurement says otherwise.
func (s *Service) queryProbes(ctx context.Context, texts []string) ([]port.Probe, error) {
	out := make([]port.Probe, len(texts))
	for i, text := range texts {
		probe, err := s.queryProbe(ctx, text)
		if err != nil {
			return nil, err
		}
		out[i] = probe
	}
	return out, nil
}

// sortOf applies the two defaults the contract names: relevance when a query was given, newest
// otherwise.
func sortOf(req catalogapi.ListListingsRequest) string {
	if req.Sort != "" {
		return req.Sort
	}
	if req.Query != "" || req.SimilarTo != nil {
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
	// Same rule, for the listing that is readable only so an existing cart line renders: saving
	// a new one is not something the reader can act on later.
	if l.DeletedAt != nil {
		return domain.ErrListingNotFound
	}
	if err := s.repo.AddFavorite(ctx, req.ActorID.Int64(), req.ID.Int64()); err != nil {
		return fmt.Errorf("add favorite: %w", err)
	}
	s.refreshInterests(ctx, req.ActorID.Int64())
	return nil
}

// refreshInterests folds the wishlist as it now stands back into the account's interest
// slots, so the next personalised feed reflects what was just saved rather than waiting on a
// sweep. Best-effort on purpose: the wishlist row is already written, and a ranking that
// could not be recomputed is a feed a few minutes behind, not a save that failed. The sweep
// finds the account again either way, since the mark is the wishlist itself.
func (s *Service) refreshInterests(ctx context.Context, accountID int64) {
	if err := s.repo.RecomputeInterests(ctx, accountID, positiveInteractionWeights()); err != nil {
		s.log.Warn("recompute interests", "account_id", accountID, "err", err)
	}
}

// positiveInteractionWeights is catalogapi.InteractionWeight narrowed to the types
// interestSignals may average — never the negative ones, which exclude a listing instead of
// weighing it (see RecomputeInterests).
func positiveInteractionWeights() map[string]float64 {
	out := make(map[string]float64, len(catalogapi.PositiveInteractionTypes))
	for _, t := range catalogapi.PositiveInteractionTypes {
		out[t] = catalogapi.InteractionWeight[t]
	}
	return out
}

// RemoveFavorite takes it off the wishlist. It does not check the listing exists: unsaving
// something already gone is the state the caller asked for, and a 404 would leave them unable
// to clean up a wishlist whose listing was deleted.
func (s *Service) RemoveFavorite(ctx context.Context, req catalogapi.FavoriteRequest) error {
	if err := s.repo.RemoveFavorite(ctx, req.ActorID.Int64(), req.ID.Int64()); err != nil {
		return fmt.Errorf("remove favorite: %w", err)
	}
	// Unsaving has to reach the slots too, and it is the one change no sweep can find: the row
	// it would have compared against is gone.
	s.refreshInterests(ctx, req.ActorID.Int64())
	return nil
}

// sweepInterests recomputes the accounts whose slots have fallen behind their wishlist. The
// same call the wishlist write makes, on the accounts the database says need it — so a pass
// on a healthy platform reads one query and writes nothing.
//
// One summary line per pass rather than one per account: this runs for ever, and an account
// that fails every time would otherwise bury whatever else the log had to say.
func sweepInterests(ctx context.Context, repo port.Repository, log *slog.Logger) {
	accounts, err := repo.StaleInterests(ctx, interestSweepBatch)
	if err != nil {
		log.Error("read stale interests", "err", err)
		return
	}
	var (
		failed int
		first  error
	)
	weights := positiveInteractionWeights()
	for _, accountID := range accounts {
		if err := repo.RecomputeInterests(ctx, accountID, weights); err != nil {
			failed++
			if first == nil {
				first = err
			}
		}
	}
	if failed > 0 {
		// The count *and* the first error: a pass that fails for every account fails for one
		// reason, and a summary carrying only the count let a statement Postgres refuses run
		// unnoticed — every recompute failing looked exactly like a busy platform.
		log.Error("interest recomputes failed", "accounts", failed, "of", len(accounts), "err", first)
	}
}

// interestSweepBatch bounds one pass. A recompute is a handful of vectors averaged in the
// database, so the cost is the number of accounts and not the size of any one wishlist; the
// rest are found by the next pass, because nothing clears the mark but the write.
const interestSweepBatch = 500
