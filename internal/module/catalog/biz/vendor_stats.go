package catalogbiz

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/samber/lo"

	catalogdb "shopnexus-server/internal/module/catalog/db/sqlc"
	inventorybiz "shopnexus-server/internal/module/inventory/biz"
	inventorydb "shopnexus-server/internal/module/inventory/db/sqlc"
	"shopnexus-server/internal/shared/validator"
)

type GetVendorStatsParams struct {
	AccountID uuid.UUID `validate:"required"`
}

type VendorStats struct {
	ProductCount  int64   `json:"product_count"`
	AverageRating float64 `json:"average_rating"`
	TotalSold     int64   `json:"total_sold"`
	ResponseRate  float64 `json:"response_rate"`
}

// GetVendorStats returns aggregate stats for a vendor: product count, average rating, and total sold.
func (b *CatalogHandler) GetVendorStats(ctx context.Context, params GetVendorStatsParams) (VendorStats, error) {
	var zero VendorStats
	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate get vendor stats: %w", err)
	}

	// Get product count + average rating from catalog DB
	stats, err := b.storage.Querier().GetVendorProductStats(ctx, params.AccountID)
	if err != nil {
		return zero, fmt.Errorf("db get vendor product stats: %w", err)
	}

	// Get total sold from inventory via vendor's SKUs
	var totalSold int64
	if stats.ProductCount > 0 {
		// Get all SPU IDs for the vendor
		spuRows, err := b.storage.Querier().SearchCountProductSpu(ctx, catalogdb.SearchCountProductSpuParams{
			AccountID: []uuid.UUID{params.AccountID},
		})
		if err != nil {
			return zero, fmt.Errorf("db list vendor spus: %w", err)
		}

		spuIDs := lo.Map(spuRows, func(r catalogdb.SearchCountProductSpuRow, _ int) uuid.UUID {
			return r.CatalogProductSpu.ID
		})

		// Get all SKUs for these SPUs
		skus, err := b.storage.Querier().ListProductSku(ctx, catalogdb.ListProductSkuParams{
			SpuId: spuIDs,
		})
		if err != nil {
			return zero, fmt.Errorf("db list vendor skus: %w", err)
		}

		if len(skus.Data) > 0 {
			skuIDs := lo.Map(skus.Data, func(s catalogdb.CatalogProductSku, _ int) uuid.UUID { return s.ID })
			stocks, err := b.inventory.ListStock(ctx, inventorybiz.ListStockParams{
				RefType: []inventorydb.InventoryStockRefType{inventorydb.InventoryStockRefTypeProductSku},
				RefID:   skuIDs,
			})
			if err != nil {
				return zero, fmt.Errorf("list vendor stock: %w", err)
			}
			for _, stock := range stocks.Data {
				totalSold += stock.Taken
			}
		}
	}

	return VendorStats{
		ProductCount:  stats.ProductCount,
		AverageRating: stats.AverageRating,
		TotalSold:     totalSold,
		ResponseRate:  0.98, // mock
	}, nil
}
