// Package port: interface the account adapter must satisfy.
package port

import (
	"context"

	"shopnexus/internal/module/account/domain"
)

type Repository interface {
	Create(ctx context.Context, a *domain.Account) error
	ExistsByEmail(ctx context.Context, email string) (bool, error)
	FindByEmail(ctx context.Context, email string) (domain.Account, error)
	FindByID(ctx context.Context, id int64) (domain.Account, error)
}
