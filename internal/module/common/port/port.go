// Package port: interface the common adapter must satisfy.
package port

import (
	"context"

	"shopnexus/internal/module/common/domain"
)

type Repository interface {
	InsertResource(ctx context.Context, r *domain.Resource) error
	ListEnabledOptions(ctx context.Context, optionType string) ([]domain.Option, error)
}
