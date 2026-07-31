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

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// jsonObject keeps a JSONB column at '{}' instead of the JSON literal null, so a reader
// never has to tell the two apart. Anything else is handed to pgx as-is and marshalled by
// its JSON codec.
//
// The map case is spelled out because a nil map boxed in an interface is *not* == nil —
// that is what silently turned an unset JSONB column into a NOT NULL violation.
func jsonObject(v any) any {
	switch m := v.(type) {
	case nil:
		return map[string]any{}
	case map[string]any:
		if m == nil {
			return map[string]any{}
		}
	}
	return v
}

// int64Array keeps a NOT NULL BIGINT[] column at '{}': a nil Go slice encodes as SQL NULL,
// which is the same trap jsonObject covers for a nil map.
func int64Array(v []int64) []int64 {
	if v == nil {
		return []int64{}
	}
	return v
}

// insertAuditLog derives the record's next version inside the same transaction as the
// change, so several facts from one Save get 1,2,3 without colliding on the unique key.
//
// Every parameter is cast: @table_name and @record_id appear in both the SELECT list and
// the WHERE of an INSERT ... SELECT, and without the casts Postgres deduces two types for
// one placeholder and refuses the statement (42P08).
func insertAuditLog(ctx context.Context, tx pgx.Tx, e port.AuditEntry) error {
	const stmt = `INSERT INTO audit_log (version, table_name, record_id, change_type, code, changed_by, diff, snapshot)
	           SELECT COALESCE(MAX(version), 0) + 1, @table_name::varchar, @record_id::bigint,
	                  @change_type::varchar, @code::varchar, @changed_by::bigint,
	                  @diff::jsonb, @snapshot::jsonb
	           FROM audit_log
	           WHERE table_name = @table_name AND record_id = @record_id`
	args := pgx.NamedArgs{
		"table_name":  e.Table,
		"record_id":   e.RecordID,
		"change_type": e.ChangeType,
		"code":        e.Code,
		"changed_by":  e.ChangedBy,
		"diff":        jsonObject(e.Diff),
		"snapshot":    jsonObject(e.Snapshot),
	}
	if _, err := tx.Exec(ctx, stmt, args); err != nil {
		return fmt.Errorf("db insert audit log: %w", err)
	}
	return nil
}

// isRestrictViolation is ON DELETE RESTRICT refusing to orphan a referencing row —
// deleting a category that still holds listings.
func isRestrictViolation(err error) bool {
	state := sqlState(err)
	return state == restrictViolation || state == foreignKeyViolation
}
