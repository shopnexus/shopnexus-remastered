// Package port: interface the trust adapter must satisfy.
package port

import (
	"context"

	"shopnexus/internal/module/trust/domain"
)

type Repository interface {
	InsertFeedback(ctx context.Context, f *domain.Feedback) error
	FindReputation(ctx context.Context, accountID int64, role string) (domain.Reputation, error)
	InsertReport(ctx context.Context, r *domain.Report) error
}
