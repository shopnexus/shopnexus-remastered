// Package port: interface the observability adapter must satisfy. Every method
// takes a whole batch — the writer consumes the bus in batches and inserts them
// as one COPY.
package port

import (
	"context"

	"shopnexus/internal/module/observability/domain"
)

type Repository interface {
	InsertHTTPRequests(ctx context.Context, samples []domain.HTTPSample) error
	InsertProviderCalls(ctx context.Context, samples []domain.ProviderCall) error
	InsertBusinessEvents(ctx context.Context, samples []domain.BusinessEvent) error
	InsertRuntimeMetrics(ctx context.Context, samples []domain.RuntimeSample) error

	// ApplyPopularityDeltas folds a batch of listing interactions into listing_popularity,
	// grouped by listing so one account viewing the same card three times in a batch is one
	// statement, not three. UPDATE-then-INSERT, never an upsert: a weight can be negative, and
	// an upsert would check a possibly-failing constraint against a row it was only ever going
	// to update.
	ApplyPopularityDeltas(ctx context.Context, events []domain.ListingInteractionEvent) error
	// PopularityOf answers the current score for a set of listings, 0 for one with no rows yet —
	// catalog reads this back to blend trending into a feed that has nothing personal to rank by.
	PopularityOf(ctx context.Context, listingIDs []int64) (map[int64]float64, error)
}
