package catalogbiz

import (
	"fmt"
	"strings"

	restate "github.com/restatedev/sdk-go"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/milvus-io/milvus/client/v2/milvusclient"

	"shopnexus-server/internal/infras/milvus"
	accountmodel "shopnexus-server/internal/module/account/model"
	catalogmodel "shopnexus-server/internal/module/catalog/model"
	catalogutil "shopnexus-server/internal/module/catalog/util"
	"shopnexus-server/internal/shared/paginate"
)

type SearchParams struct {
	paginate.Params

	Collection string
	Query      string

	// Scalar filters (applied inside Milvus before ANN ranking)
	AccountID       []uuid.UUID // vendor filter
	CategoryID      []uuid.UUID // category filter
	Tags            []string    // array_contains_any on tags
	IsEnabled       null.Bool   // active status
	PriceMin        null.Float  // minimum price (filters on price_min >= value)
	PriceMax        null.Float  // maximum price (filters on price_max <= value)
	DateCreatedFrom null.Int    // unix timestamp lower bound
	DateCreatedTo   null.Int    // unix timestamp upper bound
}

// buildFilterExpr builds a Milvus boolean expression from typed filter params.
func buildFilterExpr(params SearchParams) string {
	var clauses []string

	if len(params.AccountID) > 0 {
		ids := make([]string, len(params.AccountID))
		for i, id := range params.AccountID {
			ids[i] = id.String()
		}
		clauses = append(clauses, fmt.Sprintf("account_id in %s", toMilvusStringList(ids)))
	}
	if len(params.CategoryID) > 0 {
		ids := make([]string, len(params.CategoryID))
		for i, id := range params.CategoryID {
			ids[i] = id.String()
		}
		clauses = append(clauses, fmt.Sprintf("category_id in %s", toMilvusStringList(ids)))
	}
	if len(params.Tags) > 0 {
		clauses = append(clauses, fmt.Sprintf("array_contains_any(tags, %s)", toMilvusStringList(params.Tags)))
	}
	if params.IsEnabled.Valid {
		clauses = append(clauses, fmt.Sprintf("is_active == %t", params.IsEnabled.Bool))
	}
	if params.PriceMin.Valid {
		clauses = append(clauses, fmt.Sprintf("price_min >= %f", params.PriceMin.Float64))
	}
	if params.PriceMax.Valid {
		clauses = append(clauses, fmt.Sprintf("price_max <= %f", params.PriceMax.Float64))
	}
	if params.DateCreatedFrom.Valid {
		clauses = append(clauses, fmt.Sprintf("date_created >= %d", params.DateCreatedFrom.Int64))
	}
	if params.DateCreatedTo.Valid {
		clauses = append(clauses, fmt.Sprintf("date_created <= %d", params.DateCreatedTo.Int64))
	}

	return strings.Join(clauses, " && ")
}

// Search performs hybrid dense+sparse vector search with scalar filtering.
func (b *CatalogHandler) Search(ctx restate.Context, params SearchParams) ([]catalogmodel.ProductRecommend, error) {
	embeddings, err := b.llm.Embed(ctx, []string{params.Query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(embeddings) == 0 {
		return nil, catalogmodel.ErrNoEmbeddingsResult
	}
	emb := embeddings[0]

	pag := params.Constrain()
	limit := int(pag.Limit.Int32)
	offset := 0
	if pag.Offset().Valid {
		offset = int(pag.Offset().Int32)
	}

	filter := buildFilterExpr(params)

	// Build search requests with filters
	denseReq := milvusclient.NewAnnRequest("content_vector", limit, entity.FloatVector(emb.Dense)).
		WithOffset(offset)
	if filter != "" {
		denseReq.WithFilter(filter)
	}

	var results []milvus.SearchResult

	if emb.Sparse != nil {
		sparseVec := mapToSparseEmbedding(emb.Sparse)
		sparseReq := milvusclient.NewAnnRequest("sparse_vector", limit, sparseVec).
			WithOffset(offset)
		if filter != "" {
			sparseReq.WithFilter(filter)
		}

		reranker := milvusclient.NewWeightedReranker([]float64{
			float64(b.denseWeight),
			float64(b.sparseWeight),
		})

		results, err = b.milvus.HybridSearch(ctx, CollectionProducts,
			limit, reranker, []string{"id"},
			denseReq, sparseReq,
		)
	} else {
		results, err = b.milvus.Search(ctx, CollectionProducts,
			limit, []entity.Vector{entity.FloatVector(emb.Dense)},
			"content_vector", []string{"id"},
		)
	}
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	products := make([]catalogmodel.ProductRecommend, 0, len(results))
	for _, r := range results {
		id, err := uuid.Parse(r.ID)
		if err != nil {
			continue
		}
		products = append(products, catalogmodel.ProductRecommend{ID: id, Score: r.Score})
	}
	return products, nil
}

type GetRecommendationsParams struct {
	Account accountmodel.AuthenticatedAccount
	Limit   int32
}

// GetRecommendations returns product recommendations based on the user's interest vectors.
func (b *CatalogHandler) GetRecommendations(
	ctx restate.Context,
	params GetRecommendationsParams,
) ([]catalogmodel.ProductRecommend, error) {
	accountID := params.Account.ID.String()

	// 1. Query account interest vectors from Milvus
	rs, err := b.milvus.Query(ctx, CollectionAccounts,
		fmt.Sprintf("id == '%s'", accountID),
		accountOutputFields(),
	)
	if err != nil {
		return nil, fmt.Errorf("query account: %w", err)
	}
	if rs.ResultCount == 0 {
		return nil, nil
	}

	// 2. Build ANN search request per active interest slot
	var searchReqs []*milvusclient.AnnRequest
	var weights []float64

	for i := 1; i <= catalogutil.NumInterests; i++ {
		strengthCol := rs.GetColumn(fmt.Sprintf("strength_%d", i))
		if strengthCol == nil {
			continue
		}
		strengthF64, err := strengthCol.GetAsDouble(0)
		if err != nil || strengthF64 <= 0 {
			continue
		}

		interestCol := rs.GetColumn(fmt.Sprintf("interest_%d", i))
		if interestCol == nil {
			continue
		}
		vecAny, err := interestCol.Get(0)
		if err != nil {
			continue
		}
		vec, ok := vecAny.(entity.FloatVector)
		if !ok {
			continue
		}

		req := milvusclient.NewAnnRequest("content_vector", int(params.Limit), vec).
			WithFilter("is_active == true")
		searchReqs = append(searchReqs, req)
		weights = append(weights, strengthF64)
	}

	if len(searchReqs) == 0 {
		return nil, nil
	}

	// 3. Normalize weights so the maximum is 1.0
	maxW := weights[0]
	for _, w := range weights[1:] {
		if w > maxW {
			maxW = w
		}
	}
	if maxW > 0 {
		for i := range weights {
			weights[i] /= maxW
		}
	}

	// 4. Hybrid search with weighted ranker
	reranker := milvusclient.NewWeightedReranker(weights)
	results, err := b.milvus.HybridSearch(ctx, CollectionProducts,
		int(params.Limit), reranker, []string{"id"},
		searchReqs...,
	)
	if err != nil {
		return nil, fmt.Errorf("recommend search: %w", err)
	}

	products := make([]catalogmodel.ProductRecommend, 0, len(results))
	for _, r := range results {
		id, err := uuid.Parse(r.ID)
		if err != nil {
			continue
		}
		products = append(products, catalogmodel.ProductRecommend{ID: id, Score: r.Score})
	}
	return products, nil
}

func mapToSparseEmbedding(m map[uint32]float32) entity.SparseEmbedding {
	positions := make([]uint32, 0, len(m))
	values := make([]float32, 0, len(m))
	for k, v := range m {
		positions = append(positions, k)
		values = append(values, v)
	}
	emb, _ := entity.NewSliceSparseEmbedding(positions, values)
	return emb
}
