package catalogbiz

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/samber/lo"

	catalogdb "shopnexus-server/internal/module/catalog/db/sqlc"
	catalogmodel "shopnexus-server/internal/module/catalog/model"
	commonbiz "shopnexus-server/internal/module/common/biz"
	commondb "shopnexus-server/internal/module/common/db/sqlc"
	commonmodel "shopnexus-server/internal/module/common/model"
	inventorybiz "shopnexus-server/internal/module/inventory/biz"
	inventorydb "shopnexus-server/internal/module/inventory/db/sqlc"
)

const popularProductLimit = 4

// HydrateProductSpus turns DB SPU rows into rich models, batch-fetching the
// category, rating, tags, resources, and embedding-sync status so callers
// avoid per-row fan-out.
func (b *CatalogHandler) HydrateProductSpus(
	ctx context.Context,
	dbSpus []catalogdb.CatalogProductSpu,
) ([]catalogmodel.ProductSpu, error) {
	if len(dbSpus) == 0 {
		return []catalogmodel.ProductSpu{}, nil
	}

	spuIDs := lo.Map(dbSpus, func(s catalogdb.CatalogProductSpu, _ int) uuid.UUID { return s.ID })
	categoryIDs := lo.Map(dbSpus, func(s catalogdb.CatalogProductSpu, _ int) uuid.UUID { return s.CategoryID })

	categoriesMap := b.getCategoriesMap(ctx, categoryIDs)

	ratingMap, err := b.getRatingsMap(ctx, spuIDs)
	if err != nil {
		return nil, fmt.Errorf("list rating: %w", err)
	}

	tagsMap := b.getTagsMap(ctx, spuIDs)

	resourcesMap, err := b.common.Guaranteed().GetResources(ctx, commonbiz.GetResourcesParams{
		RefType: commondb.CommonResourceRefTypeProductSpu,
		RefIDs:  spuIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("get resources: %w", err)
	}

	syncStatuses, _ := b.storage.Querier().ListSearchSync(ctx, catalogdb.ListSearchSyncParams{
		RefId: spuIDs,
	})
	syncMap := lo.KeyBy(syncStatuses.Data, func(s catalogdb.CatalogSearchSync) uuid.UUID { return s.RefID })

	spus := make([]catalogmodel.ProductSpu, 0, len(dbSpus))
	for _, dbSpu := range dbSpus {
		specs := []catalogmodel.ProductSpecification{}
		if err := json.Unmarshal(dbSpu.Specifications, &specs); err != nil {
			return nil, fmt.Errorf("unmarshal specifications: %w", err)
		}

		spus = append(spus, catalogmodel.ProductSpu{
			CatalogProductSpu: dbSpu,
			Category:          catalogmodel.Category{CatalogCategory: categoriesMap[dbSpu.CategoryID]},
			Rating: catalogmodel.ProductRating{
				Score: ratingMap[dbSpu.ID].Score,
				Total: ratingMap[dbSpu.ID].Count,
			},
			Tags:             tagsMap[dbSpu.ID],
			Resources:        resourcesMap[dbSpu.ID],
			Specifications:   specs,
			IsStaleEmbedding: syncMap[dbSpu.ID].IsStaleEmbedding,
		})
	}

	return spus, nil
}

// HydrateProductSkus turns DB SKU rows into rich models, batch-fetching live
// stock and parsing the attributes blob.
func (b *CatalogHandler) HydrateProductSkus(
	ctx context.Context,
	dbSkus []catalogdb.CatalogProductSku,
) ([]catalogmodel.ProductSku, error) {
	if len(dbSkus) == 0 {
		return []catalogmodel.ProductSku{}, nil
	}

	stocks, err := b.inventory.ListStock(ctx, inventorybiz.ListStockParams{
		RefType: []inventorydb.InventoryStockRefType{inventorydb.InventoryStockRefTypeProductSku},
		RefID:   lo.Map(dbSkus, func(s catalogdb.CatalogProductSku, _ int) uuid.UUID { return s.ID }),
	})
	if err != nil {
		return nil, fmt.Errorf("list stock: %w", err)
	}
	stockMap := lo.KeyBy(stocks.Data, func(s inventorydb.InventoryStock) uuid.UUID { return s.RefID })

	skus := make([]catalogmodel.ProductSku, 0, len(dbSkus))
	for _, dbSku := range dbSkus {
		var attributes []catalogmodel.ProductAttribute
		if err := json.Unmarshal(dbSku.Attributes, &attributes); err != nil {
			return nil, fmt.Errorf("unmarshal attributes: %w", err)
		}

		skus = append(skus, catalogmodel.ProductSku{
			CatalogProductSku: dbSku,
			Stock:             stockMap[dbSku.ID].Stock,
			Taken:             stockMap[dbSku.ID].Taken,
			Attributes:        attributes,
		})
	}

	return skus, nil
}

// HydrateCategories turns DB category rows into rich models, attaching the
// first image of each category's popular products as representative resources.
func (b *CatalogHandler) HydrateCategories(
	ctx context.Context,
	dbCategories []catalogdb.CatalogCategory,
) ([]catalogmodel.Category, error) {
	if len(dbCategories) == 0 {
		return []catalogmodel.Category{}, nil
	}

	categoryIDs := lo.Map(dbCategories, func(c catalogdb.CatalogCategory, _ int) uuid.UUID { return c.ID })

	popularProducts, err := b.storage.Querier().
		ListPopularProductPerCategory(ctx, catalogdb.ListPopularProductPerCategoryParams{
			CategoryID:   categoryIDs,
			ProductLimit: popularProductLimit,
		})
	if err != nil {
		return nil, fmt.Errorf("hydrate categories: list popular products: %w", err)
	}

	spuIDs := lo.Map(popularProducts, func(row catalogdb.ListPopularProductPerCategoryRow, _ int) uuid.UUID {
		return row.SpuID
	})

	resourcesMap, err := b.common.Guaranteed().GetResources(ctx, commonbiz.GetResourcesParams{
		RefType: commondb.CommonResourceRefTypeProductSpu,
		RefIDs:  spuIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("hydrate categories: get resources: %w", err)
	}

	// categoryID -> first resource of each popular product
	categoryResourcesMap := make(map[uuid.UUID][]commonmodel.Resource)
	for _, row := range popularProducts {
		if res := resourcesMap[row.SpuID]; len(res) > 0 {
			categoryResourcesMap[row.CategoryID] = append(categoryResourcesMap[row.CategoryID], res[0])
		}
	}

	return lo.Map(dbCategories, func(c catalogdb.CatalogCategory, _ int) catalogmodel.Category {
		return catalogmodel.Category{
			CatalogCategory: c,
			Resources:       categoryResourcesMap[c.ID],
		}
	}), nil
}

// getTagsMap returns map[spuID][]tag for the given SPUs.
func (b *CatalogHandler) getTagsMap(ctx context.Context, spuIDs []uuid.UUID) map[uuid.UUID][]string {
	res, err := b.storage.Querier().ListProductSpuTag(ctx, catalogdb.ListProductSpuTagParams{
		SpuId: spuIDs,
	})
	if err != nil {
		zero := map[uuid.UUID][]string{}
		for _, id := range spuIDs {
			zero[id] = []string{}
		}
		return zero
	}
	return lo.GroupByMap(
		res.Data,
		func(tag catalogdb.CatalogProductSpuTag) (uuid.UUID, string) { return tag.SpuID, tag.Tag },
	)
}

// getRatingsMap returns map[spuID]rating for the given SPUs.
func (b *CatalogHandler) getRatingsMap(
	ctx context.Context,
	spuIDs []uuid.UUID,
) (map[uuid.UUID]catalogdb.ListRatingRow, error) {
	ratings, err := b.storage.Querier().ListRating(ctx, catalogdb.ListRatingParams{
		RefType: catalogdb.CatalogCommentRefTypeProductSpu,
		RefID:   spuIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("db list rating: %w", err)
	}
	return lo.KeyBy(ratings, func(r catalogdb.ListRatingRow) uuid.UUID { return r.RefID }), nil
}

// getCategoriesMap batch-fetches categories by IDs, keyed by category ID.
func (b *CatalogHandler) getCategoriesMap(
	ctx context.Context,
	categoryIDs []uuid.UUID,
) map[uuid.UUID]catalogdb.CatalogCategory {
	if len(categoryIDs) == 0 {
		return map[uuid.UUID]catalogdb.CatalogCategory{}
	}
	res, err := b.storage.Querier().ListCategory(ctx, catalogdb.ListCategoryParams{
		Id: lo.Uniq(categoryIDs),
	})
	if err != nil {
		return map[uuid.UUID]catalogdb.CatalogCategory{}
	}
	return lo.KeyBy(res.Data, func(c catalogdb.CatalogCategory) uuid.UUID { return c.ID })
}
