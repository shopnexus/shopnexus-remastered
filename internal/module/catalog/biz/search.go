package catalogbiz

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/pgvector/pgvector-go"

	accountmodel "shopnexus-server/internal/module/account/model"
	catalogdb "shopnexus-server/internal/module/catalog/db/sqlc"
	catalogmodel "shopnexus-server/internal/module/catalog/model"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/validator"
)

type SearchParams struct {
	paginate.Params

	Query string

	// Scalar filters (applied in SQL alongside ANN ranking)
	AccountID       []uuid.UUID // vendor filter
	CategoryID      []uuid.UUID // category filter
	Tags            []string    // any-tag match via product_spu_tag
	IsEnabled       null.Bool   // active status
	PriceMin        null.Float  // minimum price (any SKU >= value)
	PriceMax        null.Float  // maximum price (any SKU <= value)
	DateCreatedFrom null.Int    // unix timestamp lower bound
	DateCreatedTo   null.Int    // unix timestamp upper bound
}

// Search performs hybrid dense+sparse vector search with scalar filtering.
// pgvector handles both semantic ranking and scalar filtering in a single SQL query.
func (b *CatalogHandler) Search(ctx context.Context, params SearchParams) (paginate.PaginateResult[catalogmodel.ProductRecommend], error) {
	var zero paginate.PaginateResult[catalogmodel.ProductRecommend]

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate search params: %w", err)
	}

	embeddings, err := b.llm.Embed(ctx, []string{params.Query})
	if err != nil {
		return zero, fmt.Errorf("embed query: %w", err)
	}
	if len(embeddings) == 0 {
		return zero, catalogmodel.ErrNoEmbeddingsResult
	}
	emb := embeddings[0]

	var dateFrom, dateTo null.Time
	if params.DateCreatedFrom.Valid {
		dateFrom = null.TimeFrom(time.Unix(params.DateCreatedFrom.Int64, 0))
	}
	if params.DateCreatedTo.Valid {
		dateTo = null.TimeFrom(time.Unix(params.DateCreatedTo.Int64, 0))
	}

	// Prices are stored as integer cents; the generated params take null.Int.
	var priceMin, priceMax null.Int
	if params.PriceMin.Valid {
		priceMin = null.IntFrom(int64(params.PriceMin.Float64))
	}
	if params.PriceMax.Valid {
		priceMax = null.IntFrom(int64(params.PriceMax.Float64))
	}

	// Pool oversamples ANN candidates so post-ANN scalar filters still fill the page.
	pool := (params.Limit.Int32 + params.Offset().Int32) * 4

	res, err := b.storage.Querier().HybridSearchProduct(ctx, catalogdb.HybridSearchProductParams{
		Params:          params.Params,
		DenseWeight:     b.denseWeight,
		SparseWeight:    b.sparseWeight,
		LexicalWeight:   b.lexicalWeight,
		QueryDense:      pgvector.NewVector(emb.Dense),
		QuerySparse:     toSparseVector(emb.Sparse),
		QueryText:       params.Query,
		Pool:            pool,
		AccountID:       params.AccountID,
		CategoryID:      params.CategoryID,
		IsEnabled:       params.IsEnabled,
		DateCreatedFrom: dateFrom,
		DateCreatedTo:   dateTo,
		PriceMin:        priceMin,
		PriceMax:        priceMax,
		Tags:            params.Tags,
	})
	if err != nil {
		return zero, fmt.Errorf("hybrid search: %w", err)
	}

	products := make([]catalogmodel.ProductRecommend, 0, len(res.Data))
	for _, r := range res.Data {
		products = append(products, catalogmodel.ProductRecommend{ID: r.ID, Score: r.Score})
	}
	return paginate.PaginateResult[catalogmodel.ProductRecommend]{
		PageParams: params.Params,
		Data:       products,
		Total:      res.Total,
	}, nil
}

type GetRecommendationsParams struct {
	Account accountmodel.AuthenticatedAccount
	Limit   int32
}

// GetRecommendations returns product recommendations based on the user's interest vectors.
// It runs one ANN search per active slot, fused with strength-normalized weights
// (replaces the Milvus multi-request WeightedReranker).
func (b *CatalogHandler) GetRecommendations(
	ctx context.Context,
	params GetRecommendationsParams,
) ([]catalogmodel.ProductRecommend, error) {
	if err := validator.Validate(params); err != nil {
		return nil, fmt.Errorf("validate get recommendations params: %w", err)
	}

	ai, err := b.getAccountInterests(ctx, []uuid.UUID{params.Account.ID})
	if err != nil {
		return nil, fmt.Errorf("get account interests: %w", err)
	}
	interest, ok := ai[params.Account.ID]
	if !ok {
		return nil, nil
	}

	// Collect active slots (positive strength + non-empty vector).
	type slot struct {
		vec    []float32
		weight float32
	}
	var slots []slot
	var maxW float32
	for i := range interest.strengths {
		w := interest.strengths[i]
		if w <= 0 || len(interest.interests[i]) == 0 {
			continue
		}
		slots = append(slots, slot{vec: interest.interests[i], weight: w})
		if w > maxW {
			maxW = w
		}
	}
	if len(slots) == 0 {
		return nil, nil
	}

	// Normalize weights so the maximum is 1.0.
	if maxW > 0 {
		for i := range slots {
			slots[i].weight /= maxW
		}
	}

	scores := make(map[uuid.UUID]float32)
	for _, s := range slots {
		rows, err := b.storage.Querier().SearchProductByVector(ctx, catalogdb.SearchProductByVectorParams{
			Query: pgvector.NewVector(s.vec),
			Limit: params.Limit,
		})
		if err != nil {
			return nil, fmt.Errorf("recommend search: %w", err)
		}
		for _, r := range rows {
			scores[r.SpuID] += s.weight * r.Score
		}
	}

	products := make([]catalogmodel.ProductRecommend, 0, len(scores))
	for id, score := range scores {
		products = append(products, catalogmodel.ProductRecommend{ID: id, Score: score})
	}
	sort.Slice(products, func(i, j int) bool { return products[i].Score > products[j].Score })
	if int(params.Limit) < len(products) {
		products = products[:params.Limit]
	}
	return products, nil
}
