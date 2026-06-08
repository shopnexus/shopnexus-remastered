package catalogdb

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/jackc/pgx/v5"
	"github.com/pgvector/pgvector-go"

	"shopnexus-server/internal/shared/paginate"
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

// HybridSearchProduct runs dense+sparse ANN with weighted score fusion + scalar
// filters, paginated (offset) with a matching count. Returns the page of ranked
// spu ids plus the total matching count.
func (q *Queries) HybridSearchProduct(
	ctx context.Context,
	arg HybridSearchProductParams,
) (paginate.PaginateResult[HybridSearchProductRow], error) {
	var zero paginate.PaginateResult[HybridSearchProductRow]

	args := pgx.NamedArgs{
		"query_dense":       arg.QueryDense,
		"query_sparse":      arg.QuerySparse,
		"query_text":        arg.QueryText,
		"pool":              arg.Pool,
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

	args["rrf_k"] = rrfK

	page := hybridSearchCTE + `
SELECT spu.id,
	(@dense_weight::float4   * COALESCE(1.0 / (@rrf_k + d.rnk), 0)
	+ @sparse_weight::float4  * COALESCE(1.0 / (@rrf_k + s.rnk), 0)
	+ @lexical_weight::float4 * COALESCE(1.0 / (@rrf_k + l.rnk), 0))::float4 AS score
` + hybridSearchFromWhere + `
ORDER BY score DESC`
	if arg.Limit.Int32 > 0 {
		page += ` LIMIT @limit OFFSET @offset`
		args["limit"] = arg.Limit.Int32
		args["offset"] = arg.Offset().Int32
	}

	rows, err := q.db.Query(ctx, page, args)
	if err != nil {
		return zero, fmt.Errorf("hybrid search: %w", err)
	}
	data, err := pgx.CollectRows(rows, pgx.RowToStructByName[HybridSearchProductRow])
	if err != nil {
		return zero, fmt.Errorf("scan hybrid search: %w", err)
	}

	// Count: same CTE pools + filters, no order/limit/offset. NOT a window fn.
	var total int64
	countQuery := hybridSearchCTE + ` SELECT COUNT(*) ` + hybridSearchFromWhere
	if err = q.db.QueryRow(ctx, countQuery, args).Scan(&total); err != nil {
		return zero, fmt.Errorf("count hybrid search: %w", err)
	}

	return paginate.PaginateResult[HybridSearchProductRow]{
		PageParams: arg.Params,
		Data:       data,
		Total:      null.IntFrom(total),
	}, nil
}
