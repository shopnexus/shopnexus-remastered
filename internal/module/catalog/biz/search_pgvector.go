package catalogbiz

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"

	catalogdb "shopnexus-server/internal/module/catalog/db/sqlc"
	catalogmodel "shopnexus-server/internal/module/catalog/model"
	catalogutil "shopnexus-server/internal/module/catalog/util"
)

const (
	ContentVectorDim = 768    // MGTE dense dim; BGE-M3 = 1024 (prod switch = ALTER migration + re-embed)
	SparseVectorDim  = 250048 // XLM-R tokenizer vocab; covers both MGTE and BGE-M3 sparse indices
)

// toSparseVector converts an LLM sparse map to a pgvector sparse vector.
// Returns nil when the map is empty so the column stays NULL.
func toSparseVector(m map[uint32]float32) *pgvector.SparseVector {
	if len(m) == 0 {
		return nil
	}
	conv := make(map[int32]float32, len(m))
	for k, v := range m {
		conv[int32(k)] = v
	}
	sv := pgvector.NewSparseVectorFromMap(conv, SparseVectorDim)
	return &sv
}

// toVector converts a dense slice to a pgvector vector.
func toVector(v []float32) pgvector.Vector { return pgvector.NewVector(v) }

// getProductVectors fetches dense embeddings for the given product IDs.
func (b *CatalogHandler) getProductVectors(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]float32, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	rows, err := b.storage.Querier().GetProductVectors(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("get product vectors: %w", err)
	}

	result := make(map[uuid.UUID][]float32, len(rows))
	for _, r := range rows {
		result[r.SpuID] = r.Embedding.Slice()
	}
	return result, nil
}

// getAccountInterests fetches interest vectors and strengths for the given account IDs,
// reshaping the per-slot rows into fixed-size NumInterests slices (slot is 1-based).
func (b *CatalogHandler) getAccountInterests(
	ctx context.Context,
	ids []uuid.UUID,
) (map[uuid.UUID]accountInterests, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	rows, err := b.storage.Querier().ListAccountInterest(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list account interests: %w", err)
	}

	result := make(map[uuid.UUID]accountInterests, len(ids))
	for _, r := range rows {
		ai, ok := result[r.AccountID]
		if !ok {
			interests, strengths := catalogutil.DefaultInterests(ContentVectorDim)
			ai = accountInterests{interests: interests, strengths: strengths}
		}
		idx := int(r.Slot) - 1
		if idx < 0 || idx >= catalogutil.NumInterests {
			continue
		}
		ai.interests[idx] = r.Embedding.Slice()
		ai.strengths[idx] = r.Strength
		result[r.AccountID] = ai
	}
	return result, nil
}

// upsertAccountInterests upserts an account's NumInterests interest vectors and strengths.
func (b *CatalogHandler) upsertAccountInterests(
	ctx context.Context,
	accountID uuid.UUID,
	interests [][]float32,
	strengths []float32,
) error {
	args := make([]catalogdb.UpsertAccountInterestParams, catalogutil.NumInterests)
	for i := range catalogutil.NumInterests {
		args[i] = catalogdb.UpsertAccountInterestParams{
			AccountID: accountID,
			Slot:      int16(i + 1),
			Embedding: toVector(interests[i]),
			Strength:  strengths[i],
		}
	}

	var upsertErr error
	b.storage.Querier().UpsertAccountInterest(ctx, args).Exec(func(i int, err error) {
		if err != nil {
			upsertErr = err
		}
	})
	if upsertErr != nil {
		return fmt.Errorf("upsert account interests: %w", upsertErr)
	}
	return nil
}

// upsertProducts upserts dense+sparse embeddings for the given products.
// Products without a dense embedding are skipped (row stays absent).
func (b *CatalogHandler) upsertProducts(
	ctx context.Context,
	products []catalogmodel.ProductDetail,
	embeddings map[string]embeddingResult,
) error {
	args := make([]catalogdb.UpsertProductEmbeddingParams, 0, len(products))
	for _, p := range products {
		emb, ok := embeddings[p.ID.String()]
		if !ok || len(emb.dense) == 0 {
			continue
		}
		args = append(args, catalogdb.UpsertProductEmbeddingParams{
			SpuID:     p.ID,
			Embedding: toVector(emb.dense),
			Sparse:    toSparseVector(emb.sparse),
		})
	}
	if len(args) == 0 {
		return nil
	}

	var upsertErr error
	b.storage.Querier().UpsertProductEmbedding(ctx, args).Exec(func(i int, err error) {
		if err != nil {
			upsertErr = err
		}
	})
	if upsertErr != nil {
		return fmt.Errorf("upsert products: %w", upsertErr)
	}
	return nil
}

// upsertCategories upserts dense+sparse embeddings for the given categories.
func (b *CatalogHandler) upsertCategories(
	ctx context.Context,
	categories []catalogdb.CatalogCategory,
	embeddings map[string]embeddingResult,
) error {
	args := make([]catalogdb.UpsertCategoryEmbeddingParams, 0, len(categories))
	for _, c := range categories {
		emb, ok := embeddings[c.ID.String()]
		if !ok || len(emb.dense) == 0 {
			continue
		}
		args = append(args, catalogdb.UpsertCategoryEmbeddingParams{
			CategoryID: c.ID,
			Embedding:  toVector(emb.dense),
			Sparse:     toSparseVector(emb.sparse),
		})
	}
	if len(args) == 0 {
		return nil
	}

	var upsertErr error
	b.storage.Querier().UpsertCategoryEmbedding(ctx, args).Exec(func(i int, err error) {
		if err != nil {
			upsertErr = err
		}
	})
	if upsertErr != nil {
		return fmt.Errorf("upsert categories: %w", upsertErr)
	}
	return nil
}

// upsertTags upserts dense+sparse embeddings for the given tags.
// The embeddings map is keyed by deterministic UUID (matches search_sync ref_id scheme).
func (b *CatalogHandler) upsertTags(
	ctx context.Context,
	tags []catalogdb.CatalogTag,
	embeddings map[string]embeddingResult,
) error {
	args := make([]catalogdb.UpsertTagEmbeddingParams, 0, len(tags))
	for _, t := range tags {
		key := uuid.NewSHA1(uuid.NameSpaceURL, []byte(t.ID)).String()
		emb, ok := embeddings[key]
		if !ok || len(emb.dense) == 0 {
			continue
		}
		args = append(args, catalogdb.UpsertTagEmbeddingParams{
			TagID:     t.ID,
			Embedding: toVector(emb.dense),
			Sparse:    toSparseVector(emb.sparse),
		})
	}
	if len(args) == 0 {
		return nil
	}

	var upsertErr error
	b.storage.Querier().UpsertTagEmbedding(ctx, args).Exec(func(i int, err error) {
		if err != nil {
			upsertErr = err
		}
	})
	if upsertErr != nil {
		return fmt.Errorf("upsert tags: %w", upsertErr)
	}
	return nil
}
