package catalogbiz

import (
	"context"
	"encoding/json"
	"fmt"

	accountmodel "shopnexus-server/internal/module/account/model"
	catalogdb "shopnexus-server/internal/module/catalog/db/sqlc"
	catalogmodel "shopnexus-server/internal/module/catalog/model"
	inventorybiz "shopnexus-server/internal/module/inventory/biz"
	inventorydb "shopnexus-server/internal/module/inventory/db/sqlc"
	"shopnexus-server/internal/shared/nullutil"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	restate "github.com/restatedev/sdk-go"
)

type ListProductSkuParams struct {
	ID              []uuid.UUID `validate:"omitempty,dive,required"`
	SpuID           []uuid.UUID `validate:"omitempty"`
	PriceFrom       null.Int64  `validate:"omitnil,gt=0"`
	PriceTo         null.Int64  `validate:"omitnil,gt=0,gtefield=PriceFrom"`
	SharedPackaging null.Bool   `validate:"omitnil"`
}

// ListProductSku returns product SKUs filtered by ID, SPU, price range, or combinability.
func (b *CatalogHandler) ListProductSku(
	ctx context.Context,
	params ListProductSkuParams,
) ([]catalogmodel.ProductSku, error) {
	var zero []catalogmodel.ProductSku

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate list product sku: %w", err)
	}

	res, err := b.storage.Querier().ListProductSku(ctx, catalogdb.ListProductSkuParams{
		Id:              params.ID,
		SpuId:           params.SpuID,
		PriceFrom:       params.PriceFrom,
		PriceTo:         params.PriceTo,
		SharedPackaging: nullutil.NullBoolToSlice(params.SharedPackaging),
	})
	if err != nil {
		return zero, fmt.Errorf("db list product sku: %w", err)
	}

	skus, err := b.HydrateProductSkus(ctx, res.Data)
	if err != nil {
		return zero, fmt.Errorf("hydrate product skus: %w", err)
	}

	return skus, nil
}

type CreateProductSkuParams struct {
	Account         accountmodel.AuthenticatedAccount
	SpuID           uuid.UUID                       `validate:"required"`
	Price           int64                           `validate:"required,gt=0"`
	SharedPackaging bool                            `validate:"required"`
	Attributes      []catalogmodel.ProductAttribute `validate:"omitempty,dive"`
	PackageDetails  json.RawMessage                 `validate:"required"`
}

// CreateProductSku creates a new product SKU and initializes its inventory stock.
func (b *CatalogHandler) CreateProductSku(
	ctx restate.Context,
	params CreateProductSkuParams,
) (catalogmodel.ProductSku, error) {
	var zero catalogmodel.ProductSku

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate create product sku params: %w", err)
	}

	attributesBytes, err := json.Marshal(params.Attributes)
	if err != nil {
		return zero, fmt.Errorf("create product sku: %w", err)
	}
	packagedetailsBytes, err := json.Marshal(params.PackageDetails)
	if err != nil {
		return zero, fmt.Errorf("create product sku: %w", err)
	}

	// execution: create the SKU.
	sku, err := restate.Run(ctx, func(rctx restate.RunContext) (catalogdb.CatalogProductSku, error) {
		sku, err := b.storage.Querier().CreateDefaultProductSku(rctx, catalogdb.CreateDefaultProductSkuParams{
			SpuID:           params.SpuID,
			Price:           params.Price,
			SharedPackaging: params.SharedPackaging,
			Attributes:      attributesBytes,
			PackageDetails:  packagedetailsBytes,
		})
		if err != nil {
			return catalogdb.CatalogProductSku{}, fmt.Errorf("db create product sku: %w", err)
		}
		return sku, nil
	})
	if err != nil {
		return zero, err
	}

	// execution: initialize inventory stock (cross-module).
	if _, err := b.inventory.Call().CreateStock(ctx, inventorybiz.CreateStockParams{
		RefID:   sku.ID,
		RefType: inventorydb.InventoryStockRefTypeProductSku,
		Stock:   0,
	}); err != nil {
		return zero, fmt.Errorf("create product sku: %w", err)
	}

	skus, err := b.HydrateProductSkus(ctx, []catalogdb.CatalogProductSku{sku})
	if err != nil {
		return zero, fmt.Errorf("hydrate created product sku: %w", err)
	}
	return skus[0], nil
}

type UpdateProductSkuParams struct {
	Account         accountmodel.AuthenticatedAccount
	ID              uuid.UUID                       `validate:"required"`
	Price           null.Int                        `validate:"omitnil"`
	SharedPackaging null.Bool                       `validate:"omitnil"`
	Attributes      []catalogmodel.ProductAttribute `validate:"omitnil,dive"`
	PackageDetails  json.RawMessage                 `validate:"omitempty"`
}

// UpdateProductSku updates a product SKU and invalidates the parent SPU search index.
func (b *CatalogHandler) UpdateProductSku(
	ctx restate.Context,
	params UpdateProductSkuParams,
) (catalogmodel.ProductSku, error) {
	var zero catalogmodel.ProductSku

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate update product sku: %w", err)
	}

	attributesBytes, err := json.Marshal(params.Attributes)
	if err != nil {
		return zero, fmt.Errorf("update product sku: %w", err)
	}
	packageDetailsBytes, err := json.Marshal(params.PackageDetails)
	if err != nil {
		return zero, fmt.Errorf("update product sku: %w", err)
	}
	// TODO: check biz logic of attribute update

	// execution: update the SKU and mark the parent SPU search index stale.
	sku, err := restate.Run(ctx, func(rctx restate.RunContext) (catalogdb.CatalogProductSku, error) {
		sku, err := b.storage.Querier().UpdateProductSku(rctx, catalogdb.UpdateProductSkuParams{
			ID:              params.ID,
			Price:           params.Price,
			SharedPackaging: params.SharedPackaging,
			Attributes:      attributesBytes,
			PackageDetails:  packageDetailsBytes,
		})
		if err != nil {
			return catalogdb.CatalogProductSku{}, fmt.Errorf("db update product sku: %w", err)
		}

		// Re-embed parent product (SKU attributes feed the embedding text).
		if err := b.storage.Querier().MarkStaleSearchSync(rctx, catalogdb.MarkStaleSearchSyncParams{
			RefType: catalogdb.CatalogSearchSyncRefTypeProductSpu,
			RefID:   sku.SpuID,
		}); err != nil {
			return catalogdb.CatalogProductSku{}, fmt.Errorf("db update search sync: %w", err)
		}
		return sku, nil
	})
	if err != nil {
		return zero, err
	}

	skus, err := b.HydrateProductSkus(ctx, []catalogdb.CatalogProductSku{sku})
	if err != nil {
		return zero, fmt.Errorf("hydrate updated product sku: %w", err)
	}
	return skus[0], nil
}

type DeleteProductSkuParams struct {
	Account accountmodel.AuthenticatedAccount
	ID      uuid.UUID `validate:"required"`
}

// DeleteProductSku deletes a product SKU by ID.
func (b *CatalogHandler) DeleteProductSku(ctx restate.Context, params DeleteProductSkuParams) error {
	if err := validator.Validate(params); err != nil {
		return fmt.Errorf("validate delete product sku: %w", err)
	}

	// execution: delete the SKU.
	if err := restate.RunVoid(ctx, func(rctx restate.RunContext) error {
		if err := b.storage.Querier().DeleteProductSku(rctx, catalogdb.DeleteProductSkuParams{
			ID: []uuid.UUID{params.ID},
		}); err != nil {
			return fmt.Errorf("db delete product sku: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}

	// TODO: should delete via message queue instead
	// Delete the associated stock record
	// if err := b.storage.Querier().DeleteInventoryStock(ctx, catalogdb.DeleteInventoryStockParams{
	// 	RefType: []catalogdb.InventoryStockRefType{catalogdb.InventoryStockRefTypeProductSku},
	// 	RefID:   []uuid.UUID{params.ID},
	// }); err != nil {
	// 	return err
	// }

	return nil
}
