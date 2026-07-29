// Package port: interface the catalog adapter must satisfy.
package port

import (
	"context"

	"shopnexus/internal/module/catalog/domain"
)

type Repository interface {
	Save(ctx context.Context, l *domain.Listing) error
	FindByID(ctx context.Context, id int64) (domain.Listing, error)
	List(ctx context.Context, limit, offset int) ([]domain.Listing, error)
	UpsertStock(ctx context.Context, productID int64, quantity int64) error
	FindStock(ctx context.Context, productID int64) (int64, error)
}
