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
}
