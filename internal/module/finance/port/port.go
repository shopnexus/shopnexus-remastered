// Package port: interface the finance adapter must satisfy.
package port

import (
	"context"

	"shopnexus/internal/module/finance/domain"
)

type Repository interface {
	// NextSessionID reserves a key before the INSERT: a provider redirect URL
	// embeds the session id, so the app has to know it first.
	NextSessionID(ctx context.Context) (int64, error)
	InsertSession(ctx context.Context, s *domain.Session) error
	FindSessionByID(ctx context.Context, id int64) (domain.Session, error)
	FindWallet(ctx context.Context, accountID int64, currency string) (domain.Wallet, error)
}
