// Package postgres implements the observability port.Repository. Batches are
// written with COPY (pgx.CopyFrom) rather than INSERT: telemetry arrives in
// bulk, and COPY is the cheapest way into a hypertable.
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopnexus/internal/module/observability/domain"
	"shopnexus/internal/module/observability/port"
)

type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

var _ port.Repository = (*Repo)(nil)

func (r *Repo) InsertHTTPRequests(ctx context.Context, samples []domain.HTTPSample) error {
	cols := []string{"ts", "instance", "method", "route", "status", "duration_ms"}
	rows := make([][]any, len(samples))
	for i, s := range samples {
		rows[i] = []any{s.TS, s.Instance, s.Method, s.Route, s.Status, s.DurationMs}
	}
	return r.copy(ctx, "http_requests", cols, rows)
}

func (r *Repo) InsertProviderCalls(ctx context.Context, samples []domain.ProviderCall) error {
	cols := []string{"ts", "instance", "provider", "method", "path", "status", "duration_ms", "failed", "error"}
	rows := make([][]any, len(samples))
	for i, s := range samples {
		rows[i] = []any{s.TS, s.Instance, s.Provider, s.Method, s.Path, s.Status, s.DurationMs, s.Failed, s.Error}
	}
	return r.copy(ctx, "provider_calls", cols, rows)
}

func (r *Repo) InsertBusinessEvents(ctx context.Context, samples []domain.BusinessEvent) error {
	cols := []string{"ts", "instance", "topic", "payload"}
	rows := make([][]any, len(samples))
	for i, s := range samples {
		rows[i] = []any{s.TS, s.Instance, s.Topic, string(s.Payload)}
	}
	return r.copy(ctx, "business_events", cols, rows)
}

func (r *Repo) InsertRuntimeMetrics(ctx context.Context, samples []domain.RuntimeSample) error {
	cols := []string{"ts", "instance", "goroutines", "heap_alloc_bytes", "heap_inuse_bytes", "gc_pause_ms", "num_gc", "websocket_conns"}
	rows := make([][]any, len(samples))
	for i, s := range samples {
		rows[i] = []any{s.TS, s.Instance, s.Goroutines, s.HeapAllocBytes, s.HeapInuseBytes, s.GCPauseMs, s.NumGC, s.WebSocketConns}
	}
	return r.copy(ctx, "runtime_metrics", cols, rows)
}

func (r *Repo) copy(ctx context.Context, table string, cols []string, rows [][]any) error {
	if len(rows) == 0 {
		return nil
	}
	if _, err := r.pool.CopyFrom(ctx, pgx.Identifier{table}, cols, pgx.CopyFromRows(rows)); err != nil {
		return fmt.Errorf("db copy %s (%d rows): %w", table, len(rows), err)
	}
	return nil
}
