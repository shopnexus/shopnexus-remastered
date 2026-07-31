package postgres

import (
	"context"
	"fmt"

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
		"ids":         nullInt64Array(f.IDs),
		"variant_ids": nullInt64Array(f.VariantIDs),
		"query":       nullText(f.Query),
		"viewer_id":   f.ViewerID,
		"mine":        f.Mine,
		"favorited":   f.Favorited,
		"status":      nullText(string(f.Status)),
		"category_id": nullInt64(f.CategoryID),
		"tag":         nullText(f.Tag),
		"seller_id":   nullInt64(f.SellerID),
		"condition":   nullText(string(f.Condition)),
		"min_price":   nullInt64(f.MinPrice),
		"max_price":   nullInt64(f.MaxPrice),
		"probe":       nullText(vectorLiteralOrEmpty(f.Probe)),
		"limit":       f.Limit,
		"offset":      f.Offset,
	}
	q := feedSelect + scoreExpr(f) + feedFrom + feedWhere + orderBy(f) + feedPage
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
		if err := rows.Scan(&s.ID, &s.SellerID, &s.Slug, &s.Name, &s.Status, &s.Condition,
			&s.PriceMode, &s.Currency, &s.Price, &s.Sold, &s.Rating, &s.CategoryID,
			&s.CoverID, &s.HasPendingEdit, &s.CreatedAt, &s.DeletedAt, &s.Score,
			&total); err != nil {
			return nil, 0, fmt.Errorf("db scan listing card: %w", err)
		}
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
	                  l.price_mode::text, l.currency, COALESCE(v.price, 0), l.cached_sold,
	                  l.cached_rating, l.category_id, l.attachments[1],
	                  l.pending_edit IS NOT NULL, l.created_at, l.deleted_at,
	                  `

const feedFrom = ` AS score,
	                  COUNT(*) OVER () AS total_count
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
	               AND (@tag::text IS NULL OR EXISTS (
	                     SELECT 1 FROM listing_tag lt
	                     WHERE lt.listing_id = l.id AND lt.tag = @tag::text))
	               -- A price bound is about the variants, so it is satisfied by any one of them.
	               AND (@min_price::bigint IS NULL OR EXISTS (
	                     SELECT 1 FROM variant mv
	                     WHERE mv.listing_id = l.id AND mv.deleted_at IS NULL
	                       AND mv.price >= @min_price::bigint))
	               AND (@max_price::bigint IS NULL OR EXISTS (
	                     SELECT 1 FROM variant xv
	                     WHERE xv.listing_id = l.id AND xv.deleted_at IS NULL
	                       AND xv.price <= @max_price::bigint))
	               -- The lexical half of a search, diacritic-insensitive through
	               -- listing_name_unaccent_trgm_idx. A semantic query filters on nothing: the
	               -- ranking is the answer, and an ANN scan has no threshold to apply here.
	               AND (@query::text IS NULL OR @probe::text IS NOT NULL
	                    OR f_unaccent(l.name) % f_unaccent(@query::text))
	             END
	           )`

const feedPage = `
	           LIMIT @limit OFFSET @offset`

// scoreExpr picks what "score" means for this request. Always higher-is-better, so a client
// never has to know which mode ran:
//
//   - lexical: trigram similarity of the unaccented name.
//   - semantic and recommended: 1 − cosine distance to the probe.
//   - hybrid: the sum of the two, which is what a query with both halves is for.
//
// A listing with no embedding scores 0 on the dense half rather than dropping out, so it stays
// findable lexically — the contract says so explicitly.
func scoreExpr(f port.ListingFilter) string {
	const lexical = `similarity(f_unaccent(l.name), f_unaccent(@query::text))`
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
	default:
		return head + `l.created_at DESC` + tail
	}
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

func nullText(s string) any {
	if s == "" {
		return nil
	}
	return s
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
