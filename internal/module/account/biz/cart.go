package accountbiz

import (
	"context"
	"shopnexus-remastered/internal/db"
	promotionmodel "shopnexus-remastered/internal/module/promotion/model"
)

type GetCartParams struct {
	AccountID int64
}

type CartItem struct {
	Sku       db.CatalogProductSku
	Spu       db.CatalogProductSpu
	Promotion db.PromotionBase

	Price    int64
	Quantity int64
}

func (s *AccountBiz) GetCart(ctx context.Context, params GetCartParams) ([]CartItem, error) {
	cartItems, err := s.storage.ListAccountCartItem(ctx, db.ListAccountCartItemParams{
		CartID: []int64{params.AccountID},
	})
	if err != nil {
		return nil, nil
	}
	skuIDs := make([]int64, 0, len(cartItems))
	for _, item := range cartItems {
		skuIDs = append(skuIDs, item.SkuID)
	}

	skus, err := s.storage.ListCatalogProductSku(ctx, db.ListCatalogProductSkuParams{
		ID: skuIDs,
	})
	if err != nil {
		return nil, nil
	}
	skuMap := make(map[int64]db.CatalogProductSku)
	spuIDs := make([]int64, 0, len(skus))
	for _, sku := range skus {
		skuMap[sku.ID] = sku
		spuIDs = append(spuIDs, sku.SpuID)
	}

	spus, err := s.storage.ListCatalogProductSpu(ctx, db.ListCatalogProductSpuParams{
		ID: spuIDs,
	})
	if err != nil {
		return nil, nil
	}
	spuMap := make(map[int64]db.CatalogProductSpu) // map[spuID]SPU
	for _, spu := range spus {
		spuMap[spu.ID] = spu
	}

	// -- Calculate sale price

	// Get all active promotions
	promotions, err := s.storage.ListActivePromotion(ctx, db.ListActivePromotionParams{})
	if err != nil {
		return nil, err
	}

	// Get all applicable promotions for each product
	applicablePromotions := make(map[int64][]db.PromotionBase) // map[skuID][]Promotion
	for _, sku := range skus {
		for _, promo := range promotions {
			if promotionmodel.IsPromotionApplicable(promo, spuMap[sku.SpuID], sku.ID) {
				applicablePromotions[sku.ID] = append(applicablePromotions[sku.ID], promo)
			}
		}
	}

	// Calculate the price after applying the best promotion of each type (Voucher, Flash Sale, ...)\
	// First with voucher:
	discountPromotions, err := s.storage.ListPromotionDiscount(ctx, db.ListPromotionDiscountParams{
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

	promotionMap := make(map[int64]db.PromotionBase)
	for _, promo := range promotions {
		promotionMap[promo.ID] = promo
	}

	result := make([]CartItem, 0, len(cartItems))
	for _, item := range cartItems {
		sku := skuMap[item.SkuID]
		spu := spuMap[sku.SpuID]
		price := int64(0)

		// Get best promotion
		for _, promo := range discountPromotions {
			price = min(price, promotionmodel.CalculateDiscountedItemPrice(sku.Price, promo))
		}

		result = append(result, CartItem{
			Sku:      sku,
			Spu:      spu,
			Price:    price,
			Quantity: item.Quantity,
		})
	}

	return result, nil
}
