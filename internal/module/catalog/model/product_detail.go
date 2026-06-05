package catalogmodel

import (
	commonmodel "shopnexus-server/internal/module/common/model"

	"github.com/google/uuid"
)

type ProductDetail struct {
	ID             uuid.UUID              `json:"id"`
	Slug           string                 `json:"slug"`
	SellerID       uuid.UUID              `json:"seller_id"`
	Name           string                 `json:"name"`
	Description    string                 `json:"description"`
	IsEnabled      bool                   `json:"is_enabled"`
	Currency       string                 `json:"currency"`
	Category       Category               `json:"category"`
	Rating         ProductRating          `json:"rating"`
	IsFavorite     bool                   `json:"is_favorite"`
	Resources      []commonmodel.Resource `json:"resources"`
	Promotions     []ProductCardPromo     `json:"promotions"`
	Skus           []ProductDetailSku     `json:"skus"`
	Specifications []ProductSpecification `json:"specifications"`
	Tags           []string               `json:"tags"`
}

type ProductRating struct {
	Score     float64     `json:"score"`
	Total     int64       `json:"total"`
	Breakdown map[int]int `json:"breakdown"`
}

type ProductDetailSku struct {
	ID            uuid.UUID          `json:"id"`
	Price         int64              `json:"price"`
	OriginalPrice int64              `json:"original_price"`
	Attributes    []ProductAttribute `json:"attributes"`
	Taken         int64              `json:"taken"`
	Stock         int64              `json:"stock"`
}
