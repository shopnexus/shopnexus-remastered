// Package postgres creates *pgxpool.Pool from DSN.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool opens a pool whose connections default to the given schema via
// search_path (schema first, then public so shared extensions like pgvector /
// pg_trgm still resolve). Every module points at the same DatabaseDSN but a
// different schema, keeping their tables isolated in one database.
func NewPool(ctx context.Context, dsn, schema string) (*pgxpool.Pool, error) {
	if dsn == "" {
		return nil, errors.New("postgres: empty dsn")
	}
	if schema == "" {
		return nil, errors.New("postgres: empty schema")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse postgres dsn: %w", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ", public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return pool, nil
}
