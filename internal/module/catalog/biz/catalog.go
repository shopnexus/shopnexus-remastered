package catalogbiz

import (
	"context"

	"shopnexus-remastered/internal/db"
	sharedmodel "shopnexus-remastered/internal/module/shared/model"
	pgxsqlc "shopnexus-remastered/internal/utils/pgx/sqlc"

	"github.com/jackc/pgx/v5/pgtype"
)

type CatalogBiz struct {
	storage *pgxsqlc.Storage
}

func NewCatalogBiz(storage *pgxsqlc.Storage) *CatalogBiz {
	return &CatalogBiz{
		storage: storage,
	}
}

type ListProductParams struct {
	sharedmodel.PaginationParams
}

type Product struct {
	db.CatalogProductSpu
	Skus []ProductSku `json:"skus"`
}

type ProductSku struct {
	db.CatalogProductSku
	Attributes []db.CatalogProductSkuAttribute `json:"attributes"`
}

func (c *CatalogBiz) ListProduct(ctx context.Context, params ListProductParams) (sharedmodel.PaginateResult[Product], error) {
	var zero sharedmodel.PaginateResult[Product]

	total, err := c.storage.CountProductSpu(ctx, db.CountProductSpuParams{})
	if err != nil {
		return zero, err
	}

	spus, err := c.storage.ListProductSpu(ctx, db.ListProductSpuParams{
		Limit:  params.GetLimit(),
		Offset: params.GetOffset(),
	})
	if err != nil {
		return zero, err
	}

	products := make([]Product, 0, len(spus))
	for _, spu := range spus {
		skus, err := c.storage.ListProductSku(ctx, db.ListProductSkuParams{
			SpuID: pgtype.Int8{Int64: spu.ID, Valid: true},
		})
		if err != nil {
			return zero, err
		}

		productSkus := make([]ProductSku, 0, len(skus))
		for _, sku := range skus {
			skuAttributes, err := c.storage.ListProductSkuAttribute(ctx, db.ListProductSkuAttributeParams{
				SkuID: pgtype.Int8{Int64: sku.ID, Valid: true},
			})
			if err != nil {
				return zero, err
			}

			productSkus = append(productSkus, ProductSku{
				CatalogProductSku: sku,
				Attributes:        skuAttributes,
			})
		}

		products = append(products, Product{
			CatalogProductSpu: spu,
			Skus:              productSkus,
		})
	}

	return sharedmodel.PaginateResult[Product]{
		Data:       products,
		Limit:      params.Limit,
		Page:       params.Page,
		Total:      total,
		NextPage:   params.NextPage(total),
		NextCursor: params.NextCursor(total),
	}, nil
}
