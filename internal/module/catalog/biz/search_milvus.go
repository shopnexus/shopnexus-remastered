package catalogbiz

import (
	"context"
	"fmt"
	"strings"

	restate "github.com/restatedev/sdk-go"

	"github.com/google/uuid"
	"github.com/milvus-io/milvus/client/v2/column"
	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/milvus-io/milvus/client/v2/milvusclient"

	catalogdb "shopnexus-server/internal/module/catalog/db/sqlc"
	catalogmodel "shopnexus-server/internal/module/catalog/model"
	catalogutil "shopnexus-server/internal/module/catalog/util"
)

// emptySparseEmbedding is a reusable zero-value sparse embedding for Milvus upserts.
var emptySparseEmbedding, _ = entity.NewSliceSparseEmbedding(nil, nil)

// getProductVectors fetches content_vector for the given product IDs from Milvus.
func (b *CatalogHandler) getProductVectors(ctx restate.Context, ids []string) (map[string][]float32, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	expr := fmt.Sprintf("id in %s", toMilvusStringList(ids))
	rs, err := b.milvus.Query(ctx, CollectionProducts, expr, []string{"id", "content_vector"})
	if err != nil {
		return nil, fmt.Errorf("query product vectors: %w", err)
	}

	idCol := rs.GetColumn("id")
	vecCol := rs.GetColumn("content_vector")
	if idCol == nil || vecCol == nil {
		return nil, nil
	}

	result := make(map[string][]float32, rs.ResultCount)
	for i := range rs.ResultCount {
		id, err := idCol.GetAsString(i)
		if err != nil {
			continue
		}
		vecAny, err := vecCol.Get(i)
		if err != nil {
			continue
		}
		if vec, ok := vecAny.(entity.FloatVector); ok {
			result[id] = []float32(vec)
		}
	}
	return result, nil
}

// getAccountInterests fetches interest vectors and strengths for the given account IDs.
func (b *CatalogHandler) getAccountInterests(ctx restate.Context, ids []string) (map[string]accountInterests, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	expr := fmt.Sprintf("id in %s", toMilvusStringList(ids))
	rs, err := b.milvus.Query(ctx, CollectionAccounts, expr, accountOutputFields())
	if err != nil {
		return nil, fmt.Errorf("query account interests: %w", err)
	}

	idCol := rs.GetColumn("id")
	if idCol == nil {
		return nil, nil
	}

	result := make(map[string]accountInterests, rs.ResultCount)
	for i := range rs.ResultCount {
		id, err := idCol.GetAsString(i)
		if err != nil {
			continue
		}

		interests := make([][]float32, catalogutil.NumInterests)
		strengths := make([]float32, catalogutil.NumInterests)
		for j := range catalogutil.NumInterests {
			vecCol := rs.GetColumn(fmt.Sprintf("interest_%d", j+1))
			strCol := rs.GetColumn(fmt.Sprintf("strength_%d", j+1))

			if vecCol != nil {
				vecAny, err := vecCol.Get(i)
				if err == nil {
					if vec, ok := vecAny.(entity.FloatVector); ok {
						interests[j] = []float32(vec)
					}
				}
			}
			if interests[j] == nil {
				interests[j] = make([]float32, ContentVectorDim)
			}

			if strCol != nil {
				s, err := strCol.GetAsDouble(i)
				if err == nil {
					strengths[j] = float32(s)
				}
			}
		}
		result[id] = accountInterests{interests: interests, strengths: strengths}
	}
	return result, nil
}

// upsertAccountInterests upserts an account's interest vectors and strengths to Milvus.
func (b *CatalogHandler) upsertAccountInterests(
	ctx restate.Context,
	accountID string,
	accountNumber int64,
	interests [][]float32,
	strengths []float32,
) error {
	cols := []column.Column{
		column.NewColumnVarChar("id", []string{accountID}),
		column.NewColumnInt64("number", []int64{accountNumber}),
	}
	for i := range catalogutil.NumInterests {
		cols = append(
			cols,
			column.NewColumnFloatVector(fmt.Sprintf("interest_%d", i+1), ContentVectorDim, [][]float32{interests[i]}),
		)
		cols = append(cols, column.NewColumnFloat(fmt.Sprintf("strength_%d", i+1), []float32{strengths[i]}))
	}

	_, err := b.milvus.Inner().Upsert(ctx, milvusclient.NewColumnBasedInsertOption(CollectionAccounts, cols...))
	if err != nil {
		return fmt.Errorf("upsert account interests: %w", err)
	}
	return nil
}

// getProductAllVectors fetches content_vector and sparse_vector for the given product IDs from Milvus.
func (b *CatalogHandler) getProductAllVectors(ctx context.Context, ids []string) (map[string]existingVectors, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	expr := fmt.Sprintf("id in %s", toMilvusStringList(ids))
	rs, err := b.milvus.Query(ctx, CollectionProducts, expr, []string{"id", "content_vector", "sparse_vector"})
	if err != nil {
		return nil, fmt.Errorf("query product vectors: %w", err)
	}

	idCol := rs.GetColumn("id")
	denseCol := rs.GetColumn("content_vector")
	sparseCol := rs.GetColumn("sparse_vector")
	if idCol == nil {
		return nil, nil
	}

	result := make(map[string]existingVectors, rs.ResultCount)
	for i := range rs.ResultCount {
		id, err := idCol.GetAsString(i)
		if err != nil {
			continue
		}
		ev := existingVectors{}
		if denseCol != nil {
			if vecAny, err := denseCol.Get(i); err == nil {
				if vec, ok := vecAny.(entity.FloatVector); ok {
					ev.dense = []float32(vec)
				}
			}
		}
		if sparseCol != nil {
			if sparseAny, err := sparseCol.Get(i); err == nil {
				if sv, ok := sparseAny.(entity.SparseEmbedding); ok {
					ev.sparse = sv
				}
			}
		}
		result[id] = ev
	}
	return result, nil
}

type existingVectors struct {
	dense  []float32
	sparse entity.SparseEmbedding
}

// upsertProducts upserts product data (and optionally vectors) to Milvus.
func (b *CatalogHandler) upsertProducts(
	ctx context.Context,
	products []catalogmodel.ProductDetail,
	embeddings map[string]embeddingResult,
	metadataOnly bool,
) error {
	if len(products) == 0 {
		return nil
	}

	// For metadata-only updates, fetch existing vectors since Milvus Upsert requires all fields.
	var existingVecMap map[string]existingVectors
	if metadataOnly {
		productIDs := make([]string, len(products))
		for i, p := range products {
			productIDs[i] = p.ID.String()
		}
		var err error
		existingVecMap, err = b.getProductAllVectors(ctx, productIDs)
		if err != nil {
			return fmt.Errorf("fetch existing vectors: %w", err)
		}
	}

	ids := make([]string, 0, len(products))
	numbers := make([]int64, 0, len(products))
	accountIDs := make([]string, 0, len(products))
	categoryIDs := make([]string, 0, len(products))
	isActives := make([]bool, 0, len(products))
	priceMins := make([]float32, 0, len(products))
	priceMaxs := make([]float32, 0, len(products))
	dateCreateds := make([]int64, 0, len(products))
	tagRows := make([][]string, 0, len(products))
	denseVecs := make([][]float32, 0, len(products))
	sparseVecs := make([]entity.SparseEmbedding, 0, len(products))

	for _, p := range products {
		pid := p.ID.String()

		ids = append(ids, pid)
		numbers = append(numbers, 0)
		accountIDs = append(accountIDs, p.SellerID.String())
		categoryIDs = append(categoryIDs, p.Category.ID.String())
		isActives = append(isActives, p.IsEnabled)

		// Derive price range from SKUs
		var pMin, pMax float32
		for i, sku := range p.Skus {
			price := float32(sku.Price)
			if i == 0 || price < pMin {
				pMin = price
			}
			if i == 0 || price > pMax {
				pMax = price
			}
		}
		priceMins = append(priceMins, pMin)
		priceMaxs = append(priceMaxs, pMax)
		dateCreateds = append(dateCreateds, 0) // not available in ProductDetail; use 0
		tagRows = append(tagRows, p.Tags)

		if metadataOnly {
			if ev, ok := existingVecMap[pid]; ok {
				denseVecs = append(denseVecs, ev.dense)
				sparseVecs = append(sparseVecs, ev.sparse)
			} else {
				denseVecs = append(denseVecs, make([]float32, ContentVectorDim))
				sparseVecs = append(sparseVecs, emptySparseEmbedding)
			}
		} else {
			emb := embeddings[pid]
			denseVecs = append(denseVecs, emb.dense)
			if emb.sparse != nil {
				sparseVecs = append(sparseVecs, mapToSparseEmbedding(emb.sparse))
			} else {
				sparseVecs = append(sparseVecs, emptySparseEmbedding)
			}
		}
	}

	cols := []column.Column{
		column.NewColumnVarChar("id", ids),
		column.NewColumnInt64("number", numbers),
		column.NewColumnVarChar("account_id", accountIDs),
		column.NewColumnVarChar("category_id", categoryIDs),
		column.NewColumnBool("is_active", isActives),
		column.NewColumnFloat("price_min", priceMins),
		column.NewColumnFloat("price_max", priceMaxs),
		column.NewColumnInt64("date_created", dateCreateds),
		column.NewColumnVarCharArray("tags", tagRows),
		column.NewColumnFloatVector("content_vector", ContentVectorDim, denseVecs),
		column.NewColumnSparseVectors("sparse_vector", sparseVecs),
	}

	_, err := b.milvus.Inner().Upsert(ctx, milvusclient.NewColumnBasedInsertOption(CollectionProducts, cols...))
	if err != nil {
		return fmt.Errorf("upsert products: %w", err)
	}
	return nil
}

// upsertCategories upserts category vectors to the Milvus categories collection.
func (b *CatalogHandler) upsertCategories(
	ctx context.Context,
	categories []catalogdb.CatalogCategory,
	embeddings map[string]embeddingResult,
) error {
	if len(categories) == 0 {
		return nil
	}

	ids := make([]string, len(categories))
	denseVecs := make([][]float32, len(categories))
	sparseVecs := make([]entity.SparseEmbedding, len(categories))

	for i, c := range categories {
		cid := c.ID.String()
		ids[i] = cid

		if emb, ok := embeddings[cid]; ok {
			denseVecs[i] = emb.dense
			if emb.sparse != nil {
				sparseVecs[i] = mapToSparseEmbedding(emb.sparse)
			} else {
				sparseVecs[i] = emptySparseEmbedding
			}
		} else {
			denseVecs[i] = make([]float32, ContentVectorDim)
			sparseVecs[i] = emptySparseEmbedding
		}
	}

	cols := []column.Column{
		column.NewColumnVarChar("id", ids),
		column.NewColumnFloatVector("content_vector", ContentVectorDim, denseVecs),
		column.NewColumnSparseVectors("sparse_vector", sparseVecs),
	}

	_, err := b.milvus.Inner().Upsert(ctx, milvusclient.NewColumnBasedInsertOption(CollectionCategories, cols...))
	if err != nil {
		return fmt.Errorf("upsert categories: %w", err)
	}
	return nil
}

// upsertTags upserts tag vectors to the Milvus tags collection.
func (b *CatalogHandler) upsertTags(
	ctx context.Context,
	tags []catalogdb.CatalogTag,
	embeddings map[string]embeddingResult,
) error {
	if len(tags) == 0 {
		return nil
	}

	ids := make([]string, len(tags))
	denseVecs := make([][]float32, len(tags))
	sparseVecs := make([]entity.SparseEmbedding, len(tags))

	for i, t := range tags {
		ids[i] = t.ID

		// Tags use deterministic UUID as key in the embeddings map
		tagUUID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(t.ID))
		if emb, ok := embeddings[tagUUID.String()]; ok {
			denseVecs[i] = emb.dense
			if emb.sparse != nil {
				sparseVecs[i] = mapToSparseEmbedding(emb.sparse)
			} else {
				sparseVecs[i] = emptySparseEmbedding
			}
		} else {
			denseVecs[i] = make([]float32, ContentVectorDim)
			sparseVecs[i] = emptySparseEmbedding
		}
	}

	cols := []column.Column{
		column.NewColumnVarChar("id", ids),
		column.NewColumnFloatVector("content_vector", ContentVectorDim, denseVecs),
		column.NewColumnSparseVectors("sparse_vector", sparseVecs),
	}

	_, err := b.milvus.Inner().Upsert(ctx, milvusclient.NewColumnBasedInsertOption(CollectionTags, cols...))
	if err != nil {
		return fmt.Errorf("upsert tags: %w", err)
	}
	return nil
}

// toMilvusStringList formats a string slice as a Milvus filter expression list: ['a','b'].
func toMilvusStringList(ids []string) string {
	if len(ids) == 0 {
		return "[]"
	}
	var b strings.Builder
	b.WriteByte('[')
	for i, id := range ids {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('\'')
		b.WriteString(id)
		b.WriteByte('\'')
	}
	b.WriteByte(']')
	return b.String()
}
