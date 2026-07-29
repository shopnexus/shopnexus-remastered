// Package port: interface the order adapter must satisfy.
package port

import (
	"context"

	"shopnexus/internal/module/order/domain"
)

type Repository interface {
	Save(ctx context.Context, o *domain.Order) error
	FindByID(ctx context.Context, id int64) (domain.Order, error)
}
