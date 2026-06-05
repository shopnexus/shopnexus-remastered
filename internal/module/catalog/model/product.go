package catalogmodel

import (
	"encoding/json"
	commonmodel "shopnexus-server/internal/module/common/model"
	"time"

	"github.com/google/uuid"
)

type ProductSpu struct {
	ID            uuid.UUID     `json:"id"`
	AccountID     uuid.UUID     `json:"account_id"`
	Slug          string        `json:"slug"`
	Category      Category      `json:"category"`
	FeaturedSkuID uuid.NullUUID `json:"featured_sku_id"`
	Name          string        `json:"name"`
	Description   string        `json:"description"`
	IsEnabled     bool          `json:"is_enabled"`
	Currency      string        `json:"currency"`
	DateCreated   time.Time     `json:"date_created"`
	DateUpdated   time.Time     `json:"date_updated"`

	Rating         ProductRating          `json:"rating"`
	Tags           []string               `json:"tags"`
	Resources      []commonmodel.Resource `json:"resources"`
	Specifications []ProductSpecification `json:"specifications"`

	IsStaleEmbedding bool `json:"is_stale_embedding"`
	IsStaleMetadata  bool `json:"is_stale_metadata"`
}

type ProductSku struct {
	ID              uuid.UUID `json:"id"`
	SpuID           uuid.UUID `json:"spu_id"`
	Price           int64     `json:"price"`
	SharedPackaging bool      `json:"combinable"`
	DateCreated     time.Time `json:"date_created"`
	Stock           int64     `json:"stock"`

	Attributes     []ProductAttribute `json:"attributes"`
	PackageDetails json.RawMessage    `json:"package_details"`
}
