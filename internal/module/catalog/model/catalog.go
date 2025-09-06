package catalogmodel

import (
	"github.com/jackc/pgx/v5/pgtype"
)

type ProductCard struct {
	ID               int64              `json:"id"`
	Code             string             `json:"code"`
	VendorID         int64              `json:"vendor_id"`
	CategoryID       int64              `json:"category_id"`
	BrandID          int64              `json:"brand_id"`
	Name             string             `json:"name"`
	Description      string             `json:"description"`
	IsActive         bool               `json:"is_active"`
	DateManufactured pgtype.Timestamptz `json:"date_manufactured"`
	DateCreated      pgtype.Timestamptz `json:"date_created"`
	DateUpdated      pgtype.Timestamptz `json:"date_updated"`
	DateDeleted      pgtype.Timestamptz `json:"date_deleted"`

	Price         int64             `json:"price"`
	OriginalPrice int64             `json:"original_price"`
	Rating        Rating            `json:"rating"`
	Image         string            `json:"image,omitempty"`
	Promo         *ProductCardPromo `json:"promo,omitempty"`
}

type ProductCardPromo struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type ProductSku struct {
	ID          int64              `json:"id"`
	Code        string             `json:"code"`
	SpuID       int64              `json:"spu_id"`
	Price       int64              `json:"price"`
	CanCombine  bool               `json:"can_combine"`
	DateCreated pgtype.Timestamptz `json:"date_created"`
	DateDeleted pgtype.Timestamptz `json:"date_deleted"`

	Attributes []ProductSkuAttribute `json:"attributes"`
}

type ProductSkuAttribute struct {
	ID          int64              `json:"id"`
	Code        string             `json:"code"`
	SkuID       int64              `json:"sku_id"`
	Name        string             `json:"name"`
	Value       string             `json:"value"`
	DateCreated pgtype.Timestamptz `json:"date_created"`
	DateUpdated pgtype.Timestamptz `json:"date_updated"`
}

// FlagshipPrice is the best price for the product (currently is the lowest price of a product's SKU)
type FlagshipPrice struct {
	OriginalPrice      int64
	Price              int64
	SkuID              int64
	AppliedPromotionID *int64
}

type Rating struct {
	Score float32 `json:"score"`
	Total int     `json:"total"`
}
