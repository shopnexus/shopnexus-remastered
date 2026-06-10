package catalogdb

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/jackc/pgx/v5"
	"github.com/pgvector/pgvector-go"

	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/repolist"
)

// HybridSearchProductParams is the hand-written hybrid-search filter. It embeds
// paginate.Params (page/limit/offset) so search is paginated like every other
// list; zero Limit = fetch all (within the ANN pool).
type HybridSearchProductParams struct {
	paginate.Params

	DenseWeight   float32
	SparseWeight  float32
	LexicalWeight float32 // accent-insensitive trigram match on product name
	QueryDense    pgvector.Vector
	QuerySparse   *pgvector.SparseVector
	QueryText     string // raw query text for the lexical (unaccent + trigram) pool
	Pool          int32  // ANN candidate pool per CTE (oversample before scalar filters)

	AccountID       []uuid.UUID
	CategoryID      []uuid.UUID
	IsEnabled       null.Bool
	DateCreatedFrom null.Time
	DateCreatedTo   null.Time
	PriceMin        null.Int
	PriceMax        null.Int
	Tags            []string
}

type HybridSearchProductRow struct {
	ID    uuid.UUID `db:"id"`
	Score float32   `db:"score"`
}

// hybridSearchCTE builds the dense + sparse + lexical candidate pools, each
// emitting a per-pool rank (1 = best). Fusion is Reciprocal Rank Fusion, not a
// weighted sum of raw scores: dense/lexical scores are [0,1] but the sparse
// inner-product is unbounded, so a raw sum lets one noisy sparse hit outscore an
// exact name match. RRF fuses by rank, which is scale-free. Shared verbatim by
// the page + count queries so their candidate set can't drift.
const hybridSearchCTE = `WITH dense AS (
	SELECT spu_id, ROW_NUMBER() OVER (ORDER BY embedding <=> @query_dense::vector) AS rnk
	FROM catalog.product_embedding
	WHERE embedding IS NOT NULL
	ORDER BY embedding <=> @query_dense::vector
	LIMIT @pool::int
),
sparse AS (
	SELECT spu_id, ROW_NUMBER() OVER (ORDER BY sparse <#> @query_sparse::sparsevec) AS rnk
	FROM catalog.product_embedding
	WHERE @query_sparse::sparsevec IS NOT NULL AND sparse IS NOT NULL
	ORDER BY sparse <#> @query_sparse::sparsevec
	LIMIT @pool::int
),
lexical AS (
	-- word_similarity (not similarity): query is short, names are long, so match
	-- the query against the best extent of the name instead of the whole string.
	SELECT id AS spu_id, ROW_NUMBER() OVER (ORDER BY word_similarity(catalog.f_unaccent(@query_text), catalog.f_unaccent(name)) DESC) AS rnk
	FROM catalog.product_spu
	WHERE @query_text <> '' AND catalog.f_unaccent(name) %> catalog.f_unaccent(@query_text)
	ORDER BY word_similarity(catalog.f_unaccent(@query_text), catalog.f_unaccent(name)) DESC
	LIMIT @pool::int
)`

// rrfK dampens the rank contribution (standard RRF constant). Larger = flatter
// curve across ranks; 60 is the canonical default.
const rrfK = 60

// hybridSortExprs whitelists user-sortable fields to columns on the ranked `spu`
// alias. Compile-time only: user input selects a key, never reaches a column
// name — no injection. price/rating read the denormalized cached_* columns.
var hybridSortExprs = map[string]string{
	"id":           "spu.id",
	"date_created": "spu.date_created",
	"price":        "spu.cached_price",
	"rating":       "spu.cached_rating",
}

// hybridOrderBy turns the request sort into the ORDER BY clause (no keyword).
// Empty sort => relevance (score DESC). spu.id is the stable tiebreaker.
func hybridOrderBy(raw string) (string, error) {
	sort := paginate.ParseSort(raw)
	if len(sort) == 0 {
		return "score DESC", nil
	}
	parts := make([]string, 0, len(sort)+1)
	for _, s := range sort {
		expr, ok := hybridSortExprs[s.Field]
		if !ok {
			return "", fmt.Errorf("sort field not allowed: %q", s.Field)
		}
		dir := "ASC"
		if s.Dir == paginate.Desc {
			dir = "DESC"
		}
		parts = append(parts, expr+" "+dir+" NULLS LAST")
	}
	parts = append(parts, "spu.id ASC")
	return strings.Join(parts, ", "), nil
}

// rrfScore is the Reciprocal Rank Fusion score projection over the three pool
// ranks. Selected as a real column so the listing can order/paginate by it.
const rrfScore = `(@dense_weight::float4   * COALESCE(1.0 / (@rrf_k + d.rnk), 0)
	+ @sparse_weight::float4  * COALESCE(1.0 / (@rrf_k + s.rnk), 0)
	+ @lexical_weight::float4 * COALESCE(1.0 / (@rrf_k + l.rnk), 0))::float4 AS score`

// hybridSearchFromWhere joins the ANN pools to live product tables and applies
// scalar filters. Shared by page + count.
const hybridSearchFromWhere = `FROM catalog.product_spu spu
LEFT JOIN dense d ON d.spu_id = spu.id
LEFT JOIN sparse s ON s.spu_id = spu.id
LEFT JOIN lexical l ON l.spu_id = spu.id
WHERE (d.spu_id IS NOT NULL OR s.spu_id IS NOT NULL OR l.spu_id IS NOT NULL)
  AND spu.date_deleted IS NULL
  AND (spu.account_id = ANY(@account_id) OR @account_id IS NULL)
  AND (spu.category_id = ANY(@category_id) OR @category_id IS NULL)
  AND (spu.is_enabled = @is_enabled OR @is_enabled IS NULL)
  AND (spu.date_created >= @date_created_from OR @date_created_from IS NULL)
  AND (spu.date_created <= @date_created_to OR @date_created_to IS NULL)
  AND (EXISTS (SELECT 1 FROM catalog.product_sku sku WHERE sku.spu_id = spu.id AND sku.date_deleted IS NULL AND sku.price >= @price_min) OR @price_min IS NULL)
  AND (EXISTS (SELECT 1 FROM catalog.product_sku sku WHERE sku.spu_id = spu.id AND sku.date_deleted IS NULL AND sku.price <= @price_max) OR @price_max IS NULL)
  AND (EXISTS (SELECT 1 FROM catalog.product_spu_tag pt WHERE pt.spu_id = spu.id AND pt.tag = ANY(@tags)) OR @tags IS NULL)`

// HybridSearchProduct runs dense+sparse+lexical ANN with RRF score fusion +
// scalar filters, paginated (offset) with a matching count. The ranked rows are
// materialized as a subquery aliased `spu` so the shared list runtime layers
// SELECT/ORDER BY/LIMIT/count on top — the CTE pools stay visible to the
// subquery, and the sort clause (`spu.<col>`) resolves against the alias.
func (q *Queries) HybridSearchProduct(
	ctx context.Context,
	arg HybridSearchProductParams,
) (paginate.PaginateResult[HybridSearchProductRow], error) {
	order, err := hybridOrderBy(arg.Sort)
	if err != nil {
		return paginate.PaginateResult[HybridSearchProductRow]{}, err
	}

	args := pgx.NamedArgs{
		"query_dense":       arg.QueryDense,
		"query_sparse":      arg.QuerySparse,
		"query_text":        arg.QueryText,
		"pool":              arg.Pool,
		"rrf_k":             rrfK,
		"dense_weight":      arg.DenseWeight,
		"sparse_weight":     arg.SparseWeight,
		"lexical_weight":    arg.LexicalWeight,
		"account_id":        arg.AccountID,
		"category_id":       arg.CategoryID,
		"is_enabled":        arg.IsEnabled,
		"date_created_from": arg.DateCreatedFrom,
		"date_created_to":   arg.DateCreatedTo,
		"price_min":         arg.PriceMin,
		"price_max":         arg.PriceMax,
		"tags":              arg.Tags,
	}

	ranked := `(SELECT spu.*, ` + rrfScore + `
` + hybridSearchFromWhere + `) spu`

	// Search is offset + relevance/whitelist order, never keyset (relevance/score
	// is not a stable cursor key). Clear both Sort and Cursor so the runtime stays
	// in offset mode and honors Order; a stray ?cursor is ignored, as before.
	params := arg.Params
	params.Sort = ""
	params.Cursor = null.String{}

	return repolist.List(ctx, q.db, params, repolist.Query[HybridSearchProductRow]{
		Table: ranked,
		With:  hybridSearchCTE,
		PK:    "id",
		Order: order,
		Fields: func(m *HybridSearchProductRow) map[string]any {
			return map[string]any{"id": &m.ID, "score": &m.Score}
		},
		Args: args,
	})
}
