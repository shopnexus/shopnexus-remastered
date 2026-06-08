package catalogmodel

import (
	"time"

	"github.com/google/uuid"
)

const (
	// CacheRecommendSize is the recommend pool size K: top ranked SPU ids per account.
	CacheRecommendSize = 100

	// CacheKeyRecommendPool keys the per-account recommend pool (JSON []uuid) in KV.
	CacheKeyRecommendPool = "catalog:recommend:pool:%s"

	// RecommendPoolTTL bounds how long a built pool is reused before recompute.
	RecommendPoolTTL = 10 * time.Minute
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
