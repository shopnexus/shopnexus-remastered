// Package postgres implements the catalog port.Repository with pgx named args and
// hand-written SQL.
//
// All SQL is unqualified: the pool sets search_path to this module's schema, so a table
// name is enough and the module can later move to its own database without a rewrite.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopnexus/internal/module/catalog/port"
)

// The Postgres error codes the schema turns business rules into, so the adapter maps
// them back to the domain's own errors instead of leaking a driver error.
const (
	uniqueViolation     = "23505"
	foreignKeyViolation = "23503"
	restrictViolation   = "23001"
)

type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

var _ port.Repository = (*Repo)(nil)

// inTx runs fn in a transaction and rolls back on any error. A rollback after a
// successful commit is a no-op, which is why the deferred call needs no bookkeeping.
func (r *Repo) inTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db commit transaction: %w", err)
	}
	return nil
}

func sqlState(err error) string {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState()
	}
	return ""
}

func isUniqueViolation(err error) bool { return sqlState(err) == uniqueViolation }

// isRestrictViolation is ON DELETE RESTRICT refusing to orphan a referencing row —
// deleting a category that still holds listings.
func isRestrictViolation(err error) bool {
	state := sqlState(err)
	return state == restrictViolation || state == foreignKeyViolation
}
