package catalogbiz

import (
	"context"
	catalogmodel "shopnexus-remastered/internal/module/catalog/model"
	"shopnexus-remastered/internal/utils/pgutil"

	"shopnexus-remastered/internal/db"
	sharedmodel "shopnexus-remastered/internal/module/shared/model"

	"github.com/jackc/pgx/v5/pgtype"
)

type CatalogBiz struct {
	storage *pgutil.Storage
}

func NewCatalogBiz(storage *pgutil.Storage) *CatalogBiz {
	return &CatalogBiz{
		storage: storage,
	}
}

type ListProductParams struct {
	sharedmodel.PaginationParams
}

func (c *CatalogBiz) ListProduct(ctx context.Context, params ListProductParams) (sharedmodel.PaginateResult[catalogmodel.Product], error) {
	var zero sharedmodel.PaginateResult[catalogmodel.Product]
	var products []catalogmodel.Product

	total, err := c.storage.CountCatalogProductSpu(ctx, db.CountCatalogProductSpuParams{})
	if err != nil {
		return zero, err
	}

	// List all SPUs that user want to see
	spus, err := c.storage.ListCatalogProductSpu(ctx, db.ListCatalogProductSpuParams{
		Limit:  pgutil.Int32ToPgInt4(params.GetLimit()),
		Offset: pgutil.Int32ToPgInt4(params.GetOffset()),
	})
	if err != nil {
		return zero, err
	}

	// Get price

	//// List only some SKUs for compact data
	var skuMap = make(map[int64][]db.CatalogProductSku) // map[spuID][]SKU
	skus, err := c.storage.ListCatalogProductSku(ctx, db.ListCatalogProductSkuParams{
		SpuID: func() []int64 {
			ids := make([]int64, len(spus))
			for i, spu := range spus {
				ids[i] = spu.ID
			}
			return ids
		}(),
	})
	if err != nil {
		return zero, err
	}
	for _, sku := range skus {
		skuMap[sku.SpuID] = append(skuMap[sku.SpuID], sku)
	}

	// Calculate price
	lowestPrices, err := c.storage.LowestPriceProductSku(ctx, func() []int64 {
		ids := make([]int64, len(spus))
		for i, spu := range spus {
			ids[i] = spu.ID
		}
		return ids
	}())
	if err != nil {
		return zero, err
	}
	flagshipPrice := make(map[int64]*catalogmodel.FlagshipPrice)
	for _, lp := range lowestPrices {
		flagshipPrice[lp.SpuID] = &catalogmodel.FlagshipPrice{
			OriginalPrice: lp.Price,
			Price:         lp.Price,
			SkuID:         lp.ID,
		}
	}

	// -- Calculate sale price

	// Get all active promotions
	promotions, err := c.storage.ListActivePromotion(ctx, db.ListActivePromotionParams{})
	if err != nil {
		return zero, err
	}

	// Get all applicable promotions for each product
	applicablePromotions := make(map[int64][]db.PromotionBase) // map[spuID][]Promotion
	for _, spu := range spus {
		fp := flagshipPrice[spu.ID]
		for _, promo := range promotions {
			if catalogmodel.IsPromotionApplicable(promo, spu, fp.SkuID) {
				applicablePromotions[spu.ID] = append(applicablePromotions[spu.ID], promo)
			}
		}
	}

	// Calculate the price after applying the best promotion of each type (Voucher, Flash Sale, ...)\
	// First with voucher:
	discountPromotions, err := c.storage.ListPromotionDiscount(ctx, db.ListPromotionDiscountParams{
		ID: func() []int64 {
			var ids []int64
			for _, promo := range promotions {
				if promo.Type == db.PromotionTypeDiscount {
					ids = append(ids, promo.ID)
				}
			}
			return ids
		}(),
	})

	for _, spu := range spus {
		fp := flagshipPrice[spu.ID]

		for _, promo := range discountPromotions {
			fp.Price = min(catalogmodel.CalculateDiscountedPrice(fp.OriginalPrice, promo), fp.Price)
		}
	}

	for _, spu := range spus {
		products = append(products, catalogmodel.Product{
			ID:               spu.ID,
			Code:             spu.Code,
			VendorID:         spu.AccountID,
			CategoryID:       spu.CategoryID,
			BrandID:          spu.BrandID,
			Name:             spu.Name,
			Description:      spu.Description,
			IsActive:         false,
			DateManufactured: pgtype.Timestamptz{},
			DateCreated:      pgtype.Timestamptz{},
			DateUpdated:      pgtype.Timestamptz{},
			DateDeleted:      pgtype.Timestamptz{},

			Price:         flagshipPrice[spu.ID].Price,
			OriginalPrice: flagshipPrice[spu.ID].OriginalPrice,

			Skus: nil,
		})
	}

	// List some attributes for compact data
	return sharedmodel.PaginateResult[catalogmodel.Product]{
		Data:       products,
		Limit:      params.GetLimit(),
		Page:       params.GetPage(),
		Total:      total,
		NextPage:   params.NextPage(total),
		NextCursor: params.NextCursor(total),
	}, nil
}

type ListProductSpuParams struct {
	sharedmodel.PaginationParams
	Code       []string
	AccountID  []int64
	CategoryID []int64
	BrandID    []int64
	IsActive   []bool
}

func (c *CatalogBiz) ListProductSpu(ctx context.Context, params ListProductSpuParams) (sharedmodel.PaginateResult[db.CatalogProductSpu], error) {
	var zero sharedmodel.PaginateResult[db.CatalogProductSpu]

	total, err := c.storage.CountCatalogProductSpu(ctx, db.CountCatalogProductSpuParams{
		Code:       params.Code,
		AccountID:  params.AccountID,
		CategoryID: params.CategoryID,
		BrandID:    params.BrandID,
		IsActive:   params.IsActive,
	})
	if err != nil {
		return zero, err
	}

	spus, err := c.storage.ListCatalogProductSpu(ctx, db.ListCatalogProductSpuParams{
		Limit:      pgutil.Int32ToPgInt4(params.GetLimit()),
		Offset:     pgutil.Int32ToPgInt4(params.GetOffset()),
		Code:       params.Code,
		AccountID:  params.AccountID,
		CategoryID: params.CategoryID,
		BrandID:    params.BrandID,
		IsActive:   params.IsActive,
	})
	if err != nil {
		return zero, err
	}

	return sharedmodel.PaginateResult[db.CatalogProductSpu]{
		Data:       spus,
		Limit:      params.GetLimit(),
		Page:       params.GetPage(),
		Total:      total,
		NextPage:   params.NextPage(total),
		NextCursor: params.NextCursor(total),
	}, nil
}

type ListProductSkuParams struct {
	sharedmodel.PaginationParams
	Code       []string
	SpuID      []int64
	SpuIDFrom  *int64
	SpuIDTo    *int64
	Price      []int64
	PriceFrom  *int64
	PriceTo    *int64
	CanCombine []bool
}

func (c *CatalogBiz) ListProductSku(ctx context.Context, params ListProductSkuParams) (sharedmodel.PaginateResult[db.CatalogProductSku], error) {
	var zero sharedmodel.PaginateResult[db.CatalogProductSku]

	total, err := c.storage.CountCatalogProductSku(ctx, db.CountCatalogProductSkuParams{
		Code:       params.Code,
		SpuID:      params.SpuID,
		SpuIDFrom:  pgutil.PtrToPgtype(params.SpuIDFrom, pgutil.Int64ToPgInt8),
		SpuIDTo:    pgutil.PtrToPgtype(params.SpuIDTo, pgutil.Int64ToPgInt8),
		Price:      params.Price,
		PriceFrom:  pgutil.PtrToPgtype(params.PriceFrom, pgutil.Int64ToPgInt8),
		PriceTo:    pgutil.PtrToPgtype(params.PriceTo, pgutil.Int64ToPgInt8),
		CanCombine: params.CanCombine,
	})
	if err != nil {
		return zero, err
	}

	skus, err := c.storage.ListCatalogProductSku(ctx, db.ListCatalogProductSkuParams{
		Limit:      pgutil.Int32ToPgInt4(params.GetLimit()),
		Offset:     pgutil.Int32ToPgInt4(params.GetOffset()),
		Code:       params.Code,
		SpuID:      params.SpuID,
		SpuIDFrom:  pgutil.PtrToPgtype(params.SpuIDFrom, pgutil.Int64ToPgInt8),
		SpuIDTo:    pgutil.PtrToPgtype(params.SpuIDTo, pgutil.Int64ToPgInt8),
		Price:      params.Price,
		PriceFrom:  pgutil.PtrToPgtype(params.PriceFrom, pgutil.Int64ToPgInt8),
		PriceTo:    pgutil.PtrToPgtype(params.PriceTo, pgutil.Int64ToPgInt8),
		CanCombine: params.CanCombine,
	})
	if err != nil {
		return zero, err
	}

	return sharedmodel.PaginateResult[db.CatalogProductSku]{
		Data:       skus,
		Limit:      params.GetLimit(),
		Page:       params.GetPage(),
		Total:      total,
		NextPage:   params.NextPage(total),
		NextCursor: params.NextCursor(total),
	}, nil
}

type ListProductSkuAttributeParams struct {
	sharedmodel.PaginationParams
	Name []string
}

func (c *CatalogBiz) ListProductSkuAttribute(ctx context.Context, params ListProductSkuAttributeParams) (sharedmodel.PaginateResult[db.CatalogProductSkuAttribute], error) {
	var zero sharedmodel.PaginateResult[db.CatalogProductSkuAttribute]

	total, err := c.storage.CountCatalogProductSkuAttribute(ctx, db.CountCatalogProductSkuAttributeParams{
		Name: params.Name,
	})
	if err != nil {
		return zero, err
	}

	attrs, err := c.storage.ListCatalogProductSkuAttribute(ctx, db.ListCatalogProductSkuAttributeParams{
		Limit:  pgutil.Int32ToPgInt4(params.GetLimit()),
		Offset: pgutil.Int32ToPgInt4(params.GetOffset()),
		Name:   params.Name,
	})
	if err != nil {
		return zero, err
	}

	return sharedmodel.PaginateResult[db.CatalogProductSkuAttribute]{
		Data:       attrs,
		Limit:      params.GetLimit(),
		Page:       params.GetPage(),
		Total:      total,
		NextPage:   params.NextPage(total),
		NextCursor: params.NextCursor(total),
	}, nil
}
