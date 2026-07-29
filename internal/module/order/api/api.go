// Package orderapi is the published contract of the order service.
package orderapi

import (
	"context"

	"shopnexus/internal/shared/id"
)

type PlaceOrderRequest struct {
	BuyerID id.ID[id.Account] `json:"-" validate:"required"`
	Total   int64             `json:"total" validate:"required,gt=0"`
}

type GetOrderRequest struct {
	ID id.ID[id.Order] `validate:"required"`
}

type Order struct {
	ID      id.ID[id.Order]   `json:"id"`
	BuyerID id.ID[id.Account] `json:"buyer_id"`
	Total   int64             `json:"total"`
	Status  string            `json:"status"`
}

type Service interface {
	PlaceOrder(ctx context.Context, req PlaceOrderRequest) (Order, error)
	GetOrder(ctx context.Context, req GetOrderRequest) (Order, error)
}
