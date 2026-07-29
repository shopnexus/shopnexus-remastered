// Package catalogapi is the published contract of the catalog service.
package catalogapi

import (
	"context"

	"shopnexus/internal/shared/id"
)

type Seller struct {
	ID          id.ID[id.Account] `json:"id"`
	DisplayName string            `json:"display_name"`
}

type Listing struct {
	ID     id.ID[id.ProductSPU] `json:"id"`
	Title  string               `json:"title"`
	Price  int64                `json:"price"`
	Status string               `json:"status"`
	Seller Seller               `json:"seller"`
}

type CreateListingRequest struct {
	OwnerID id.ID[id.Account] `json:"-" validate:"required"`
	Title   string            `json:"title" validate:"required,max=140"`
	Price   int64             `json:"price" validate:"required,gt=0"`
}

type GetListingRequest struct {
	ID id.ID[id.ProductSPU] `validate:"required"`
}

type ListListingsRequest struct {
	Limit  int `validate:"gte=0,lte=100"`
	Offset int `validate:"gte=0"`
}

type Stock struct {
	ProductID id.ID[id.ProductSKU] `json:"product_id"`
	Quantity  int64                `json:"quantity"`
}

type SetStockRequest struct {
	ProductID id.ID[id.ProductSKU] `json:"product_id" validate:"required"`
	Quantity  int64                `json:"quantity" validate:"gte=0"`
}

type GetStockRequest struct {
	ProductID id.ID[id.ProductSKU] `validate:"required"`
}

type Service interface {
	CreateListing(ctx context.Context, req CreateListingRequest) (Listing, error)
	GetListing(ctx context.Context, req GetListingRequest) (Listing, error)
	ListListings(ctx context.Context, req ListListingsRequest) ([]Listing, error)
	SetStock(ctx context.Context, req SetStockRequest) (Stock, error)
	GetStock(ctx context.Context, req GetStockRequest) (Stock, error)
}
