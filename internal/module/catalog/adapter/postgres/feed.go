package postgres

import (
	"context"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/module/catalog/port"
	"shopnexus/internal/module/common/dbx"
)

// ListListings is the browse feed, the search and the wishlist page in one statement. The
// parameters narrow one query rather than selecting between endpoints, so every filter is a
// `@param IS NULL OR …` branch and the shape stays static SQL.
//
// A search is the exception and has its own statement (search.go): its ranking is two index-served
// ANN scans fused by rank, which is a shape no `IS NULL OR` branch can fold into a browse.
func (r *Repo) ListListings(ctx context.Context, f port.ListingFilter) ([]port.ListingSummary, int64, error) {
	// Terms that can rank nothing — a probe with both halves empty — fall through to the browse
	// rather than answering an error the shopper cannot act on.
	if q, args, ok := r.searchStatement(f); ok {
		return r.search(ctx, q, args)
	}
	args := feedArgs(f)
	q := feedSelect + freshScore + feedScore + feedTotal + feedFrom + feedWhere + orderBy(f) + feedPage
	switch {
	// The personalised feed, which is the only shape that ranks against more than one probe.
	case f.Sort == port.SortRecommended && len(f.Interests) > 0:
		probes, weights := probeArrays(f.Interests)
		args["probes"] = probes
		args["weights"] = weights
		args["fresh_weight"] = domain.FreshWeight
		args["sharpness"] = domain.ExploreSharpness
		args["seed"] = f.Seed
		// Several pages deep per source, because the merge samples the pool rather than
		// taking the top of it: a pool the size of the page is a pool with no alternatives
		// in it, and the reader would get the same twelve cards in a shuffled order.
		args["candidates"] = (f.Offset + f.Limit) * domain.FreshPoolFactor
		q = recommendedHead +
			feedSelect + recommendedScore + feedScore + feedFrom + feedWhere +
			recommendedWhere + recommendedEmbedded + recommendedFresh +
			feedSelect + freshScore + feedScore + feedFrom + feedWhere +
			recommendedWhere + recommendedTail
	}
	rows, err := r.pool.Query(ctx, q, args)
	if err != nil {
		return nil, 0, fmt.Errorf("db query listings: %w", err)
	}
	defer rows.Close()
	return scanListingCards(rows)
}

// feedArgs is every filter as parameters, shared by the browse statement and the search's ANN
// legs — which carry the same feedWhere, so a filter the two spelled differently would be a
// search that narrows unlike the browse it came from.
func feedArgs(f port.ListingFilter) pgx.NamedArgs {
	return pgx.NamedArgs{
		"ids":           nullInt64Array(f.IDs),
		"variant_ids":   nullInt64Array(f.VariantIDs),
		"query":         dbx.NullText(f.Query),
		"viewer_id":     f.ViewerID,
		"mine":          f.Mine,
		"favorited":     f.Favorited,
		"status":        dbx.NullText(string(f.Status)),
		"category_id":   nullInt64(f.CategoryID),
		"tag":           dbx.NullText(f.Tag),
		"seller_id":     nullInt64(f.SellerID),
		"condition":     dbx.NullText(string(f.Condition)),
		"min_price":     nullInt64(f.MinPrice),
		"max_price":     nullInt64Ptr(f.MaxPrice),
		"province_code": dbx.NullText(f.ProvinceCode),
		"district_code": dbx.NullText(f.DistrictCode),
		"ward_code":     dbx.NullText(f.WardCode),
		"near_lat":      nullFloat(near(f, func(p port.Point) float64 { return p.Latitude })),
		"near_lon":      nullFloat(near(f, func(p port.Point) float64 { return p.Longitude })),
		"radius_m":      nullFloat(radiusMetres(f)),
		"limit":         f.Limit,
		"offset":        f.Offset,
	}
}

// ListListingsByIDs is the personalised feed cache's hydration read: the ids came from an
// earlier draw, so this asks only for their current, live state — active listings only, no
// score, no position to measure a distance from. The caller reorders.
func (r *Repo) ListListingsByIDs(ctx context.Context, ids []int64) ([]port.ListingSummary, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	const q = feedSelect + freshScore + feedScore + `, 0::bigint AS total_count` + feedFrom + `
	           WHERE l.id = ANY(@ids::bigint[]) AND l.deleted_at IS NULL AND l.status = 'active'`
	args := pgx.NamedArgs{"ids": ids, "near_lat": nil, "near_lon": nil}
	rows, err := r.pool.Query(ctx, q, args)
	if err != nil {
		return nil, fmt.Errorf("db query listings by id: %w", err)
	}
	defer rows.Close()
	out, _, err := scanListingCards(rows)
	return out, err
}

// scanListingCards is the row shape every feed query answers in: a plain browse, a
// reranked search and the personalised draw all select the same columns, `total_count`
// included even where the caller ignores it.
func scanListingCards(rows pgx.Rows) ([]port.ListingSummary, int64, error) {
	var (
		out   []port.ListingSummary
		total int64
	)
	for rows.Next() {
		var s port.ListingSummary
		// An unpublished listing has no location at all, so every one of its columns is NULL —
		// scanned into pointers and only then assembled, because the destination strings are not
		// nullable and a card for a draft still has to render.
		var area nullableLocation
		if err := rows.Scan(&s.ID, &s.SellerID, &s.Slug, &s.Name, &s.Status, &s.Condition,
			&s.PriceMode, &s.Currency, &s.Price, &s.Sold, &s.Rating, &s.ReviewCount, &s.CategoryID,
			&s.CoverID, &area.provinceCode, &area.provinceName, &area.districtCode,
			&area.districtName, &area.wardCode, &area.wardName, &area.latitude, &area.longitude,
			&s.DistanceKM, &s.Tags, &s.TakenDownAt, &s.CreatedAt, &s.DeletedAt, &s.Score,
			&total); err != nil {
			return nil, 0, fmt.Errorf("db scan listing card: %w", err)
		}
		s.Location = area.location()
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("db iterate listings: %w", err)
	}
	return out, total, nil
}

// The price on a card is the cheapest live variant's, read through variant_price_idx in the
// lateral join rather than from a column on the listing — a cached "from" price is a second
// fact to keep in step with every variant edit.
// Every column is aliased, including the ones a bare reference would have named anyway: the
// personalised draw selects these back out of a CTE by name and a reranked search orders by
// them, and an expression's implicit name is a detail of the parser rather than a promise.
const feedSelect = `SELECT l.id AS id, l.account_id AS seller_id, l.slug AS slug, l.name AS name,
	                  l.status::text AS status, l.condition::text AS condition,
	                  l.price_mode::text AS price_mode, l.currency AS currency,
	                  COALESCE(v.price, 0) AS price, l.cached_sold AS cached_sold,
	                  l.cached_rating AS cached_rating, l.cached_review_count AS cached_review_count,
	                  l.category_id AS category_id, l.attachments[1] AS cover_id,
	                  l.province_code AS province_code, l.province_name AS province_name,
	                  l.district_code AS district_code, l.district_name AS district_name,
	                  l.ward_code AS ward_code, l.ward_name AS ward_name,
	                  ST_Y(l.location::geometry) AS latitude, ST_X(l.location::geometry) AS longitude,
	                  ` + distanceExpr + ` AS distance_km,
	                  ` + cardTags + ` AS tags,
	                  l.taken_down_at AS taken_down_at,
	                  l.created_at AS created_at, l.deleted_at AS deleted_at,
	                  `

// cardColumns is that same list read back out of a CTE, in the order the row scanner expects.
const cardColumns = `id, seller_id, slug, name, status, condition, price_mode, currency, price,
	                  cached_sold, cached_rating, cached_review_count, category_id, cover_id,
	                  province_code, province_name, district_code, district_name, ward_code,
	                  ward_name, latitude, longitude, distance_km, tags, taken_down_at,
	                  created_at, deleted_at`

// cardTags is the listing's tags on the card itself. A subquery rather than a second round trip:
// a feed page is one statement, and chips a client renders are not worth a query per row.
// COALESCE, so a listing with no tags is an empty array rather than a NULL a scanner has to hold.
const cardTags = `COALESCE((SELECT array_agg(lt.tag ORDER BY lt.tag)
	                          FROM listing_tag lt WHERE lt.listing_id = l.id), '{}')`

// distanceExpr is the kilometres between the buyer and the goods, NULL when either end has no
// point: a browse that named no position, or a listing whose address was never geocoded. Computed
// on the geography, so it is metres on the spheroid rather than degrees on a flat map.
const distanceExpr = `CASE WHEN @near_lat::double precision IS NULL OR l.location IS NULL THEN NULL
	                       ELSE ST_Distance(l.location,
	                              ST_SetSRID(ST_MakePoint(@near_lon::double precision,
	                                                      @near_lat::double precision),
	                                         4326)::geography) / 1000
	                  END`

const feedScore = ` AS score`

// feedTotal is the count behind the page. Its own piece because a relevance sort has no seekable
// total to count — a top-K is not a set somebody can page through (see fusedTotal).
const feedTotal = `,
	                  COUNT(*) OVER () AS total_count`

const feedFrom = `
	           FROM listing l
	           LEFT JOIN LATERAL (
	             SELECT price FROM variant
	             WHERE listing_id = l.id AND deleted_at IS NULL
	             ORDER BY price
	             LIMIT 1
	           ) v ON true
	           LEFT JOIN listing_embedding e ON e.listing_id = l.id`

// feedWhere is every filter at once. `ids` short-circuits the visibility rules the way the
// contract says: an order that references a hidden or deleted listing still has to render it,
// and only "never public" stays out unless the caller owns it.
const feedWhere = `
	           WHERE (
	             CASE WHEN @variant_ids::bigint[] IS NOT NULL THEN
	               -- Resolving a variant to its listing: same visibility rule as an id
	               -- lookup, because the caller already holds a reference to it.
	               EXISTS (
	                 SELECT 1 FROM variant rv
	                 WHERE rv.listing_id = l.id AND rv.id = ANY(@variant_ids::bigint[])
	               )
	               AND (l.status NOT IN ('draft', 'pending') OR l.account_id = @viewer_id)
	             WHEN @ids::bigint[] IS NOT NULL THEN
	               l.id = ANY(@ids::bigint[])
	               AND (l.status NOT IN ('draft', 'pending') OR l.account_id = @viewer_id)
	             ELSE
	               l.deleted_at IS NULL
	               -- mine=true is the only way to see what is not public, and only one's own.
	               AND CASE WHEN @mine::boolean THEN
	                     l.account_id = @viewer_id
	                     AND (@status::text IS NULL OR l.status::text = @status::text)
	                   ELSE l.status = 'active' END
	               AND (NOT @favorited::boolean OR EXISTS (
	                     SELECT 1 FROM favorite fv
	                     WHERE fv.listing_id = l.id AND fv.account_id = @viewer_id))
	               AND (@category_id::bigint IS NULL OR l.category_id = @category_id::bigint)
	               AND (@seller_id::bigint IS NULL OR l.account_id = @seller_id::bigint)
	               AND (@condition::text IS NULL OR l.condition::text = @condition::text)
	               -- Where the goods are. One level, the one the caller named: a ward code is
	               -- already inside its province, so narrowing by both would be the same filter
	               -- twice.
	               AND (@province_code::text IS NULL OR l.province_code = @province_code::text)
	               AND (@district_code::text IS NULL OR l.district_code = @district_code::text)
	               AND (@ward_code::text IS NULL OR l.ward_code = @ward_code::text)
	               -- Near me, through listing_location_gist. A listing with no point is out of a
	               -- radius rather than treated as nearby: it cannot claim a distance it has no
	               -- way to know.
	               AND (@radius_m::double precision IS NULL
	                    OR (l.location IS NOT NULL
	                        AND ST_DWithin(l.location,
	                              ST_SetSRID(ST_MakePoint(@near_lon::double precision,
	                                                      @near_lat::double precision),
	                                         4326)::geography,
	                              @radius_m::double precision)))
	               AND (@tag::text IS NULL OR EXISTS (
	                     SELECT 1 FROM listing_tag lt
	                     WHERE lt.listing_id = l.id AND lt.tag = @tag::text))
	               -- A price bound is about the variants, so it is satisfied by any one of them.
	               -- min_price stays a plain int64: 0 excludes no price a listing could have
	               -- (every price is gte=1), so it is a genuine no-op and needs no pointer.
	               AND (@min_price::bigint IS NULL OR EXISTS (
	                     SELECT 1 FROM variant mv
	                     WHERE mv.listing_id = l.id AND mv.deleted_at IS NULL
	                       AND mv.price >= @min_price::bigint))
	               AND (@max_price::bigint IS NULL OR EXISTS (
	                     SELECT 1 FROM variant xv
	                     WHERE xv.listing_id = l.id AND xv.deleted_at IS NULL
	                       AND xv.price <= @max_price::bigint))
	             END
	           )`

const feedPage = `
	           LIMIT @limit OFFSET @offset`

// The personalised feed, and the one query shape that reads the catalogue more than once.
//
// A buyer is not one taste averaged together — somebody who saves phones, running shoes and
// houseplants has three, and the mean of three directions in embedding space points at none
// of them. So each interest searches on its own, one more source offers whatever was posted
// most recently, and the results are merged: a listing's place is its rank within the source
// that liked it best, divided by that source's share of the page. A taste worth half the
// signal takes about half the page, and the smallest of the four still reaches it — where
// scoring every listing against every interest and keeping the best would hand the whole page
// to whichever interest happens to have the tightest neighbourhood.
//
// Each interest's own pass is an ordinary nearest-neighbour scan, so the vector index serves
// it exactly as it serves a semantic search.
const recommendedHead = `WITH probe AS (
	           SELECT ordinality::int AS slot, vec::vector AS vec, weight
	           FROM unnest(@probes::text[], @weights::double precision[])
	                WITH ORDINALITY AS t(vec, weight, ordinality)
	         ), hit AS (
	           SELECT p.slot, p.weight, c.*,
	                  row_number() OVER (PARTITION BY p.slot ORDER BY c.score DESC, c.id DESC) AS rank
	           FROM probe p
	           CROSS JOIN LATERAL (
	           `

// recommendedWhere is what a personalised feed excludes on top of the ordinary visibility
// rules. Nobody can buy their own goods, a listing the account already saved is one they have
// found — putting it back on the page spends a card telling them what they told us — and one
// they marked "not interested" or "hidden" is excluded outright rather than down-weighted:
// interestSignals never lets those two types near the positive average it feeds, precisely so
// this is the only place they act.
const recommendedWhere = `
	             AND l.account_id <> @viewer_id
	             AND NOT EXISTS (SELECT 1 FROM favorite rf
	                             WHERE rf.listing_id = l.id AND rf.account_id = @viewer_id)
	             AND NOT EXISTS (SELECT 1 FROM listing_signal rs
	                             WHERE rs.listing_id = l.id AND rs.account_id = @viewer_id
	                               AND rs.type IN ('not-interested', 'hidden'))
	             -- A query here is a filter on the name and never a ranking: this feed ranks against
	             -- the account's interest vectors, which have nothing to do with the words, so it
	             -- is the one path a search's terms never reach. No index and none needed — the
	             -- per-interest legs have already cut the pool to @candidates. f_unaccent on both
	             -- sides because this is also the one path that sees raw typing: a probe was
	             -- normalised by the understanding stage, and this was not.
	             AND (@query::text IS NULL
	                  OR f_unaccent(l.name) ILIKE '%' || f_unaccent(@query::text) || '%')`

// An interest can only rank what it can measure the distance to. The fresh source below
// deliberately does not carry this: the newest thing on the platform is exactly the one whose
// vector has not been computed yet.
const recommendedEmbedded = `
	             AND e.dense IS NOT NULL`

const recommendedFresh = `
	             ORDER BY e.dense <=> p.vec
	             LIMIT @candidates
	           ) c
	         ), fresh AS (
	           SELECT 0 AS slot, @fresh_weight::double precision AS weight, f.*,
	                  row_number() OVER (ORDER BY f.created_at DESC, f.id DESC) AS rank
	           FROM (
	           `

// recommendedTail merges the sources and draws the page out of the pool.
//
// The draw is weighted sampling without replacement (Efraimidis–Spirakis): giving a listing a
// weight w and ordering by `−ln(u)/w` is exactly a draw in which the odds of coming up are
// proportional to w. Here w is the source's share over the listing's rank in it, so the best
// matches usually come first and something from four pages down surfaces now and then — which
// is the whole point. A feed that always answers the same twelve cards has no way to learn it
// was wrong about somebody, and nothing new can ever get in front of them.
//
// `u` is a hash of the seed and the listing's id, never `random()`: the ordering has to be a
// pure function of the seed or the second page would be drawn from a different feed than the
// first, and a reader paging down would see the same card twice and miss others entirely.
const recommendedTail = `
	             ORDER BY l.created_at DESC
	             LIMIT @candidates
	           ) f
	         ), drawn AS (
	           SELECT s.*,
	                  power(s.rank, @sharpness::double precision) / s.weight * (-ln(
	                    ((hashtextextended(@seed::text || ':' || s.id::text, 0) & 2147483647)::double precision
	                       + 1) / 2147483649.0)) AS position
	           FROM (SELECT * FROM hit UNION ALL SELECT * FROM fresh) s
	         ), merged AS (
	           -- One row per listing, at the position of whichever source drew it highest.
	           SELECT DISTINCT ON (id) * FROM drawn ORDER BY id, position
	         )
	         SELECT ` + cardColumns + `, score, 0::bigint AS total_count
	         FROM merged
	         ORDER BY position, score DESC NULLS LAST, id DESC` + feedPage

// recommendedScore is the similarity to the interest this pass is running for. Higher is
// better, like every other score a card carries.
const recommendedScore = `1 - (e.dense <=> p.vec)`

// freshScore is what a card drawn for being new carries: nothing. It was not matched against
// anything, and a number here would be a claim about a relevance nobody measured.
const freshScore = `NULL::double precision`

// rerankOrderBy orders a pool that was built by relevance, addressing it by output column name:
// the caller's sort applies to what the ranking kept, so every name here has to be an alias
// feedSelect declares.
func rerankOrderBy(f port.ListingFilter) string {
	const head = `
	           ORDER BY `
	const tail = `, id DESC`
	switch f.Sort {
	case port.SortRating:
		return head + `cached_rating DESC` + tail
	case port.SortPriceAsc:
		return head + `price ASC` + tail
	case port.SortPriceDesc:
		return head + `price DESC` + tail
	case port.SortBestSelling:
		return head + `cached_sold DESC` + tail
	case port.SortDistance:
		return head + `distance_km ASC NULLS LAST` + tail
	default:
		return head + `created_at DESC` + tail
	}
}

// probeArrays splits the interests into the two parallel arrays the merge unnests. Text
// rather than a vector array: pgvector has no array-of-vector type, and a per-row cast from
// the literal is what every other probe in this file already crosses the wire as.
func probeArrays(interests []port.Interest) ([]string, []float64) {
	probes := make([]string, 0, len(interests))
	weights := make([]float64, 0, len(interests))
	for _, in := range interests {
		probes = append(probes, vectorLiteral(in.Vector))
		weights = append(weights, in.Weight)
	}
	return probes, weights
}

// orderBy maps the sort to a fixed expression. Nothing here is built from user input — the
// switch is the whitelist, and an unknown sort is newest.
func orderBy(f port.ListingFilter) string {
	const head = `
	           ORDER BY `
	// A score sort needs the same tiebreak as the rest, or two equal hits swap between pages.
	const tail = `, l.id DESC`
	switch f.Sort {
	case port.SortRating:
		return head + `l.cached_rating DESC` + tail
	case port.SortPriceAsc:
		return head + `COALESCE(v.price, 0) ASC` + tail
	case port.SortPriceDesc:
		return head + `COALESCE(v.price, 0) DESC` + tail
	case port.SortBestSelling:
		// cached_sold, because a sum over the variants has no per-variant index to scan in
		// order — the one sort that cannot be answered from variant_price_idx.
		return head + `l.cached_sold DESC` + tail
	case port.SortRelevance:
		return head + `score DESC NULLS LAST` + tail
	case port.SortDistance:
		// Nearest first, and a listing with no point last rather than first — NULLS LAST, because
		// "distance unknown" is not "distance zero".
		return head + distanceColumn + ` ASC NULLS LAST` + tail
	default:
		return head + `l.created_at DESC` + tail
	}
}

// nullableLocation is the location columns as they come back: all NULL for a listing that was
// never published, all set for one that was.
type nullableLocation struct {
	provinceCode *string
	provinceName *string
	districtCode *string
	districtName *string
	wardCode     *string
	wardName     *string
	latitude     *float64
	longitude    *float64
}

// location assembles the domain value, or nil when there is none. The province code is what says
// which: the column is written together with the rest or not at all.
func (n nullableLocation) location() *domain.Location {
	if n.provinceCode == nil {
		return nil
	}
	area := domain.Location{
		ProvinceCode: *n.provinceCode,
		DistrictCode: n.districtCode,
		DistrictName: n.districtName,
		Latitude:     n.latitude,
		Longitude:    n.longitude,
	}
	if n.provinceName != nil {
		area.ProvinceName = *n.provinceName
	}
	if n.wardCode != nil {
		area.WardCode = *n.wardCode
	}
	if n.wardName != nil {
		area.WardName = *n.wardName
	}
	return &area
}

// distanceColumn is the alias the ORDER BY sorts on, so the expression is written once.
const distanceColumn = `distance_km`

// near reads one coordinate of the browse position, or NaN when there is none.
func near(f port.ListingFilter, pick func(port.Point) float64) float64 {
	if f.Near == nil {
		return math.NaN()
	}
	return pick(*f.Near)
}

// radiusMetres is the bound in metres, NaN when the caller wants every distance ranked rather than
// a circle: a radius without a position is not a filter, so both have to be there.
func radiusMetres(f port.ListingFilter) float64 {
	if f.Near == nil || f.RadiusKM <= 0 {
		return math.NaN()
	}
	return f.RadiusKM * 1000
}

// nullFloat keeps a NaN out of the query as SQL NULL, which is how every other optional filter
// arrives — a NaN reaching Postgres is a comparison that is neither true nor false.
func nullFloat(v float64) any {
	if math.IsNaN(v) {
		return nil
	}
	return v
}

// FavoritedAmong reads the whole page's wishlist state at once. An empty set short-circuits: a
// page of nothing needs no query.
func (r *Repo) FavoritedAmong(ctx context.Context, accountID int64, listingIDs []int64) (map[int64]bool, error) {
	out := make(map[int64]bool, len(listingIDs))
	if accountID == 0 || len(listingIDs) == 0 {
		return out, nil
	}
	const q = `SELECT listing_id FROM favorite
	           WHERE account_id = @account_id AND listing_id = ANY(@ids)`
	rows, err := r.pool.Query(ctx, q, pgx.NamedArgs{"account_id": accountID, "ids": listingIDs})
	if err != nil {
		return nil, fmt.Errorf("db query favorited among: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var listingID int64
		if err := rows.Scan(&listingID); err != nil {
			return nil, fmt.Errorf("db scan favorited row: %w", err)
		}
		out[listingID] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate favorited: %w", err)
	}
	return out, nil
}

// AddFavorite is idempotent, which is what makes PUT the right verb: saving twice is saving
// once, and a client retrying a lost response gets the state it asked for.
func (r *Repo) AddFavorite(ctx context.Context, accountID, listingID int64) error {
	const q = `INSERT INTO favorite (account_id, listing_id) VALUES (@account_id, @listing_id)
	           ON CONFLICT (account_id, listing_id) DO NOTHING`
	args := pgx.NamedArgs{"account_id": accountID, "listing_id": listingID}
	if _, err := r.pool.Exec(ctx, q, args); err != nil {
		if dbx.IsRestrictViolation(err) {
			return domain.ErrListingNotFound
		}
		return fmt.Errorf("db insert favorite: %w", err)
	}
	return nil
}

// RemoveFavorite says nothing about whether the row was there: unsaving what is not saved
// leaves the caller in the state they asked for.
func (r *Repo) RemoveFavorite(ctx context.Context, accountID, listingID int64) error {
	const q = `DELETE FROM favorite WHERE account_id = @account_id AND listing_id = @listing_id`
	args := pgx.NamedArgs{"account_id": accountID, "listing_id": listingID}
	if _, err := r.pool.Exec(ctx, q, args); err != nil {
		return fmt.Errorf("db delete favorite: %w", err)
	}
	return nil
}

// The optional-bound helpers. A zero id or an empty string is "not filtered", which the SQL
// spells as NULL so one statement serves every combination.
func nullInt64(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

// nullInt64Ptr is nullInt64's pointer half: absence is nil, not zero, so a genuine
// `max_price=0` — a valid bound that matches nothing, since every price is gte=1 — is not
// silently read as "not filtered".
func nullInt64Ptr(n *int64) any {
	if n == nil {
		return nil
	}
	return *n
}

func nullInt64Array(v []int64) any {
	if len(v) == 0 {
		return nil
	}
	return v
}
