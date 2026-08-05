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
// Two things are not branches, because they change what is being read rather than which rows
// come back: the score expression and the ORDER BY. Both are picked from a fixed set below —
// no user input reaches either.
func (r *Repo) ListListings(ctx context.Context, f port.ListingFilter) ([]port.ListingSummary, int64, error) {
	args := pgx.NamedArgs{
		"ids":              nullInt64Array(f.IDs),
		"variant_ids":      nullInt64Array(f.VariantIDs),
		"query":            dbx.NullText(f.Query),
		"viewer_id":        f.ViewerID,
		"mine":             f.Mine,
		"favorited":        f.Favorited,
		"status":           dbx.NullText(string(f.Status)),
		"category_id":      nullInt64(f.CategoryID),
		"tag":              dbx.NullText(f.Tag),
		"seller_id":        nullInt64(f.SellerID),
		"condition":        dbx.NullText(string(f.Condition)),
		"min_price":        nullInt64(f.MinPrice),
		"max_price":        nullInt64Ptr(f.MaxPrice),
		"probe":            dbx.NullText(vectorLiteralOrEmpty(f.Probe)),
		"probe_from_query": f.ProbeFromQuery,
		"province_code":    dbx.NullText(f.ProvinceCode),
		"district_code":    dbx.NullText(f.DistrictCode),
		"ward_code":        dbx.NullText(f.WardCode),
		"near_lat":         nullFloat(near(f, func(p port.Point) float64 { return p.Latitude })),
		"near_lon":         nullFloat(near(f, func(p port.Point) float64 { return p.Longitude })),
		"radius_m":         nullFloat(radiusMetres(f)),
		"limit":            f.Limit,
		"offset":           f.Offset,
	}
	q := feedSelect + scoreExpr(f) + feedScore + feedTotal + feedFrom + feedWhere + orderBy(f) + feedPage
	// "Newest, but still about what I searched for." A ranked search has no WHERE gate — the
	// ranking is what makes it a search — so ordering it by anything other than the score
	// answers the whole catalogue in date order, which is what the query was supposed to
	// narrow. So: rank first, keep what is relevant, and order *those* by what was asked for.
	if rerankedSort(f) {
		args["candidates"] = relevantCandidates
		args["relevance_floor"] = relevanceFloor
		q = candidateHead + feedSelect + scoreExpr(f) + feedScore + feedFrom + feedWhere +
			candidateOrder + candidateTail + rerankOrderBy(f) + feedPage
	}
	rows, err := r.pool.Query(ctx, q, args)
	if err != nil {
		return nil, 0, fmt.Errorf("db query listings: %w", err)
	}
	defer rows.Close()

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
const feedSelect = `SELECT l.id, l.account_id, l.slug, l.name, l.status::text, l.condition::text,
	                  l.price_mode::text, l.currency, COALESCE(v.price, 0) AS price, l.cached_sold,
	                  l.cached_rating, l.cached_review_count, l.category_id, l.attachments[1],
	                  l.province_code, l.province_name, l.district_code, l.district_name,
	                  l.ward_code, l.ward_name,
	                  ST_Y(l.location::geometry), ST_X(l.location::geometry),
	                  ` + distanceExpr + ` AS distance_km,
	                  ` + cardTags + `,
	                  l.taken_down_at,
	                  l.created_at, l.deleted_at,
	                  `

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

// feedTotal is the count behind the page. Its own piece because the reranked search counts
// what survived the relevance floor instead, and that is not known until the pool is built.
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
	               -- The lexical half of a search, diacritic-insensitive through
	               -- listing_name_unaccent_trgm_idx. Skipped only when the probe is the
	               -- query's own embedding (a real semantic or hybrid search, where the
	               -- ranking is the answer and an ANN scan has no threshold to apply) —
	               -- never for a recommended feed's probe, which comes from the account's
	               -- interest vectors and has nothing to do with @query.
	               -- The operator is word similarity, query on the left, matched against the
	               -- best run of words in the name. Whole-string similarity compared the two
	               -- as wholes and so matched nothing a shopper ever types (see scoreExpr).
	               -- The same GIN index serves it, commuted to f_unaccent(name) %> query.
	               AND (@query::text IS NULL
	                    OR (@probe::text IS NOT NULL AND @probe_from_query::boolean)
	                    OR f_unaccent(@query::text) <<% f_unaccent(l.name))
	             END
	           )`

const feedPage = `
	           LIMIT @limit OFFSET @offset`

// relevantCandidates is how deep "relevant" goes when a search is ordered by something other
// than relevance. A rank and not a score, because a cosine score is not comparable between
// queries: on this catalogue the 300th hit for a broad query outscores the 5th for a narrow
// one, so any fixed cutoff floods the first search and empties the second. A rank asks the
// question that has a stable answer — the most relevant N — and the sort then orders those.
const relevantCandidates = 200

// relevanceFloor is the share of the top hit's score a listing must reach to stay in that pool.
// Measured, not guessed: the genuine hits for a narrow query sit at 0.93–1.00 of the best and
// the first unrelated one at 0.47, while a broad or cross-language query stays above 0.71 all
// the way down — so 0.6 falls in the gap for both shapes.
const relevanceFloor = 0.6

// rerankedSort is a ranked search asked to come back in some other order. Only then: a
// relevance sort is already the ranking, and a query with no probe still has its lexical
// WHERE gate, so both narrow on their own.
func rerankedSort(f port.ListingFilter) bool {
	return f.ProbeFromQuery &&
		f.Sort != port.SortRelevance && f.Sort != port.SortRecommended
}

const candidateHead = `WITH candidate AS (
	           `

// candidateOrder ranks the pool. The same expression the relevance sort uses, because "the
// most relevant N" has to mean the same thing whichever order they are then shown in.
const candidateOrder = `
	           ORDER BY score DESC NULLS LAST, id DESC`

// candidateTail cuts the pool down to what is actually about the query, then counts what is
// left — so the total is the pageable set and not the pool.
//
// The floor is a fraction of the *best* hit rather than a fixed score, because a cosine score
// means nothing on its own: on this catalogue a broad query's 100th hit scores 0.71 of its top
// while a narrow query's 5th scores 0.47 of its. Relative, the same number reads the shape of
// each query — it keeps a hundred shirts when a hundred shirts match, and cuts a search for
// one phone at the cliff after the four listings that mention it.
const candidateTail = `
	           LIMIT @candidates
	         ), relevant AS (
	           SELECT * FROM candidate
	           WHERE score >= @relevance_floor::double precision * (SELECT max(score) FROM candidate)
	         )
	         SELECT *, COUNT(*) OVER () FROM relevant`

// rerankOrderBy orders the candidate pool, addressing it by output column name — inside the
// CTE the row is joined from three tables, outside it is one flat row.
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

// scoreExpr picks what "score" means for this request. Always higher-is-better, so a client
// never has to know which mode ran:
//
//   - lexical: how well the query matches the best-matching run of words in the name.
//   - semantic and recommended: 1 − cosine distance to the probe.
//   - hybrid: the sum of the two, which is what a query with both halves is for.
//
// A listing with no embedding scores 0 on the dense half rather than dropping out, so it stays
// findable lexically — the contract says so explicitly.
//
// Word similarity, not whole-string `similarity`: the latter divides shared trigrams by the
// union of *both* strings, so a two-word query against a forty-character product name scores
// about 0.05 and no realistic search ever clears the 0.3 threshold — "iphone" matched none of
// the four listings with iPhone in the name. This measures the query against the best-matching
// run of words inside the name instead, which is the question a search box actually asks.
// Argument order is load-bearing: the query is the needle.
//
// And *strict*, which extends the match to whole word boundaries. Unaccenting Vietnamese makes
// "quần áo" into "quan ao", and "quan" sits inside "quang" — so the loose form scored reflective
// "phản quang" decals 0.63 against a clothing search, high enough to survive the relevance floor.
// Strict scores them 0.40 while real matches stay at 1.00, and costs 8 hits of recall in 131.
func scoreExpr(f port.ListingFilter) string {
	const lexical = `strict_word_similarity(f_unaccent(@query::text), f_unaccent(l.name))`
	const dense = `COALESCE(1 - (e.dense <=> @probe::vector), 0)`
	switch {
	case f.Probe != nil && f.Query != "" && f.Mode == port.ModeHybrid:
		return lexical + ` + ` + dense
	case f.Probe != nil:
		return dense
	case f.Query != "":
		return lexical
	default:
		return `NULL::double precision`
	}
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
	case port.SortRelevance, port.SortRecommended:
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

// InterestVectors reads the account's slots. A handful of rows by primary key: the ANN search
// runs against listing_embedding, not here.
func (r *Repo) InterestVectors(ctx context.Context, accountID int64) ([]port.Vector, error) {
	const q = `SELECT dense::text FROM account_interest
	           WHERE account_id = @account_id
	           ORDER BY strength DESC, slot`
	rows, err := r.pool.Query(ctx, q, pgx.NamedArgs{"account_id": accountID})
	if err != nil {
		return nil, fmt.Errorf("db query interest vectors: %w", err)
	}
	defer rows.Close()

	var out []port.Vector
	for rows.Next() {
		var literal string
		if err := rows.Scan(&literal); err != nil {
			return nil, fmt.Errorf("db scan interest vector: %w", err)
		}
		v, err := parseVector(literal)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate interest vectors: %w", err)
	}
	return out, nil
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

func vectorLiteralOrEmpty(v port.Vector) string {
	if len(v) == 0 {
		return ""
	}
	return vectorLiteral(v)
}
