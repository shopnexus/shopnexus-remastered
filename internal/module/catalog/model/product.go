package catalogmodel

import (
	catalogdb "shopnexus-server/internal/module/catalog/db/sqlc"
	commonmodel "shopnexus-server/internal/module/common/model"
)

// ProductSpu is the domain-layer product (SPU): the DB row plus its hydrated
// category, rating, tags, resources, parsed specifications, and embedding
// freshness. The custom Specifications field shadows the raw json.RawMessage
// promoted from CatalogProductSpu.
type ProductSpu struct {
	catalogdb.CatalogProductSpu

	Category         Category               `json:"category"`
	Rating           ProductRating          `json:"rating"`
	Tags             []string               `json:"tags"`
	Resources        []commonmodel.Resource `json:"resources"`
	Specifications   []ProductSpecification `json:"specifications"`
	IsStaleEmbedding bool                   `json:"is_stale_embedding"`
}

// ProductSku is the domain-layer SKU: the DB row plus live stock and parsed
// attributes. The custom Attributes field shadows the raw json.RawMessage
// promoted from CatalogProductSku.
type ProductSku struct {
	catalogdb.CatalogProductSku

	Stock      int64              `json:"stock"`
	Taken      int64              `json:"taken"` // reserved or sold count, from inventory stock
	Attributes []ProductAttribute `json:"attributes"`
}
