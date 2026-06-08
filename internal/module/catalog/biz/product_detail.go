package catalogbiz

import (
	"database/sql"
	"errors"
	"fmt"

	restate "github.com/restatedev/sdk-go"

	accountbiz "shopnexus-server/internal/module/account/biz"
	accountmodel "shopnexus-server/internal/module/account/model"
	analyticbiz "shopnexus-server/internal/module/analytic/biz"
	analyticmodel "shopnexus-server/internal/module/analytic/model"
	catalogdb "shopnexus-server/internal/module/catalog/db/sqlc"
	catalogmodel "shopnexus-server/internal/module/catalog/model"
	inventorybiz "shopnexus-server/internal/module/inventory/biz"
	inventorydb "shopnexus-server/internal/module/inventory/db/sqlc"
	promotionbiz "shopnexus-server/internal/module/promotion/biz"
	promotionmodel "shopnexus-server/internal/module/promotion/model"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/samber/lo"
)

type GetProductDetailParams struct {
	Account *accountmodel.AuthenticatedAccount // optional, for view tracking
	ID      uuid.NullUUID
	Slug    null.String
}

// GetProductDetail returns full product detail including SKUs, pricing, ratings, and promotions.
func (b *CatalogHandler) GetProductDetail(
	ctx restate.Context,
	params GetProductDetailParams,
) (catalogmodel.ProductDetail, error) {
	var zero catalogmodel.ProductDetail

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate get product detail params: %w", err)
	}

	spu, err := b.GetProductSpu(ctx, GetProductSpuParams{
		ID:   params.ID,
		Slug: params.Slug,
	})
	if err != nil {
		return zero, fmt.Errorf("get product spu: %w", err)
	}

	var skusDetail []catalogmodel.ProductDetailSku
	skus, err := b.ListProductSku(ctx, ListProductSkuParams{
		SpuID: []uuid.UUID{spu.ID},
	})
	if err != nil {
		return zero, fmt.Errorf("list product sku: %w", err)
	}
	skuIDs := lo.Map(skus, func(s catalogmodel.ProductSku, _ int) uuid.UUID { return s.ID })

	// Get sold count from inventory
	listStock, err := b.inventory.ListStock(ctx, inventorybiz.ListStockParams{
		RefType: []inventorydb.InventoryStockRefType{inventorydb.InventoryStockRefTypeProductSku},
		RefID:   skuIDs,
	})
	if err != nil {
		return zero, fmt.Errorf("list stock: %w", err)
	}
	stockMap := lo.KeyBy(listStock.Data, func(s inventorydb.InventoryStock) uuid.UUID { return s.RefID })

	// Calculate promoted prices for SKUs
	requestPrices := make([]catalogmodel.RequestOrderPrice, 0, len(skus))
	for _, sku := range skus {
		requestPrices = append(requestPrices, catalogmodel.RequestOrderPrice{
			SkuID:     sku.ID,
			SpuID:     sku.SpuID,
			UnitPrice: sku.Price,
			Quantity:  1,
			ShipCost:  0,
		})
	}

	priceMap, err := b.promotion.CalculatePromotedPrices(
		ctx,
		promotionbiz.CalculatePromotedPricesParams{Prices: requestPrices, SpuMap: map[uuid.UUID]promotionmodel.PromoSpu{
			spu.ID: {ID: spu.ID, CategoryID: spu.Category.ID},
		}},
	)
	if err != nil {
		return zero, fmt.Errorf("calculate promoted prices: %w", err)
	}

	for _, sku := range skus {
		priceValue := sku.Price
		originalPrice := sku.Price
		if priceInfo, ok := priceMap[sku.ID]; ok && priceInfo != nil {
			originalPrice = priceInfo.Request.UnitPrice
			if priceInfo.ProductCost != 0 {
				priceValue = priceInfo.ProductCost
			}
		}

		var taken int64
		var stockCount int64
		if stock, ok := stockMap[sku.ID]; ok {
			taken = stock.Taken
			stockCount = stock.Stock
		}

		skusDetail = append(skusDetail, catalogmodel.ProductDetailSku{
			ID:            sku.ID,
			Price:         priceValue,
			OriginalPrice: originalPrice,
			Attributes:    sku.Attributes,
			Taken:         taken,
			Stock:         stockCount,
		})
	}

	// Reuse resources and tags already fetched by GetProductSpu → ListProductSpu
	resources := spu.Resources
	tags := spu.Tags

	// Detail rating (with per-score breakdown — not available from ListProductSpu)
	rating, err := b.storage.Querier().DetailRating(ctx, catalogdb.DetailRatingParams{
		RefType: catalogdb.CatalogCommentRefTypeProductSpu,
		RefID:   spu.ID,
	})
	ratingBreakdown := make(map[int]int)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return zero, fmt.Errorf("db detail rating: %w", err)
	}
	ratingBreakdown[5] = int(rating.FiveCount)
	ratingBreakdown[4] = int(rating.FourCount)
	ratingBreakdown[3] = int(rating.ThreeCount)
	ratingBreakdown[2] = int(rating.TwoCount)
	ratingBreakdown[1] = int(rating.OneCount)

	promoSet := make(map[string]struct{})
	var promotions []catalogmodel.ProductCardPromo
	for _, price := range priceMap {
		if price == nil {
			continue
		}
		for _, code := range price.PromotionCodes {
			if _, exists := promoSet[code]; exists {
				continue
			}
			promoSet[code] = struct{}{}
			promotions = append(promotions, catalogmodel.ProductCardPromo{
				Title: code,
			})
		}
	}

	// Check favorite for authenticated user
	var isFavorite bool
	if params.Account != nil {
		favoriteSet, _ := b.account.CheckFavorites(
			ctx,
			accountbiz.CheckFavoritesParams{AccountID: params.Account.ID, SpuIDs: []uuid.UUID{spu.ID}},
		)
		isFavorite = favoriteSet[spu.ID]
	}

	// Track view interaction for authenticated users
	if params.Account != nil {
		if err := b.analytic.Send().CreateInteraction(ctx, analyticbiz.CreateInteractionParams{
			Interactions: []analyticbiz.CreateInteraction{{
				Account:   *params.Account,
				EventType: analyticmodel.EventView,
				RefType:   analyticmodel.InteractionRefTypeProduct,
				RefID:     spu.ID.String(),
			}},
		}); err != nil {
			return zero, fmt.Errorf("track view interaction: %w", err)
		}
	}

	return catalogmodel.ProductDetail{
		ID:          spu.ID,
		Slug:        spu.Slug,
		SellerID:    spu.AccountID,
		Name:        spu.Name,
		Description: spu.Description,
		IsEnabled:   spu.IsEnabled,
		Currency:    spu.Currency,
		Category:    spu.Category,
		Rating: catalogmodel.ProductRating{
			Score:     rating.Score,
			Total:     rating.Count,
			Breakdown: ratingBreakdown,
		},
		IsFavorite:     isFavorite,
		Resources:      resources,
		Promotions:     promotions,
		Skus:           skusDetail,
		Specifications: spu.Specifications,
		Tags:           tags,
	}, nil
}
