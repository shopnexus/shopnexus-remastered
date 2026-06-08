package catalogmodel

import (
	"github.com/google/uuid"
)

const (
	CacheRecommendSize       = 100
	CacheKeyRecommendProduct = "catalog:recommend:product:%s"
	CacheKeyRecommendOffset  = "catalog:recommend:offset:%s"
)

// OrderPrice is the final price of a order after applying promotions.
type OrderPrice struct {
	Request RequestOrderPrice

	ProductCost int64
	ShipCost    int64

	PromotionCodes []string
}

type RequestOrderPrice struct {
	SkuID          uuid.UUID
	SpuID          uuid.UUID
	UnitPrice      int64
	Quantity       int64
	ShipCost       int64
	PromotionCodes []string
}

type Rating struct {
	Score float32 `json:"score"`
	Total int     `json:"total"`
}
