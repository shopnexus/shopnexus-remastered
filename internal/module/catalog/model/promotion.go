package catalogmodel

import (
	"shopnexus-remastered/internal/db"
)

// FlagshipPrice is the best price for the product (currently is the lowest price of a product's SKU)
type FlagshipPrice struct {
	OriginalPrice int64
	Price         int64
	SkuID         int64
}

func IsPromotionApplicable(promo db.PromotionBase, spu db.CatalogProductSpu, skuID int64) bool {
	if !promo.RefID.Valid {
		return promo.RefType == db.PromotionRefTypeAll
	}

	refID := promo.RefID.Int64
	switch promo.RefType {
	case db.PromotionRefTypeCategory:
		return refID == spu.CategoryID
	case db.PromotionRefTypeBrand:
		return refID == spu.BrandID
	case db.PromotionRefTypeProductSpu:
		return refID == spu.ID
	case db.PromotionRefTypeProductSku:
		return refID == skuID
	case db.PromotionRefTypeAll:
		return true // shouldn't happen since RefID should be null for "all"
	default:
		return false
	}
}

func CalculateDiscountedPrice(originalPrice int64, discount db.PromotionDiscount) int64 {
	discountedPrice := originalPrice

	if discount.DiscountPercent.Valid {
		discountAmount := originalPrice * int64(discount.DiscountPercent.Int32) / 100
		discountedPrice -= min(discountAmount, discount.MaxDiscount)
	} else if discount.DiscountPrice.Valid {
		discountedPrice -= min(discount.DiscountPrice.Int64, discount.MaxDiscount)
	}

	if discountedPrice < 0 {
		return 0
	}
	return discountedPrice
}
