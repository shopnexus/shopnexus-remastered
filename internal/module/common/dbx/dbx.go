// Package dbx is the pgx layer every module's adapter shares: the transaction helper, the
// SQLSTATE predicates, the NULL and JSONB normalisations, and the audit-log write.
//
// Each of these used to be redeclared per module — isUniqueViolation in four, inTx and
// jsonObject in two apiece — so a fix landed in one copy and not the rest. The
// nil-map-boxed-in-an-interface trap in JSONObject is the one that already cost a NOT NULL
// violation in production code.
package dbx

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopnexus/internal/module/common"
)

// The Postgres error codes the schemas turn business rules into, so an adapter maps them back
// to its own domain errors instead of leaking a driver error.
const (
	uniqueViolation     = "23505"
	foreignKeyViolation = "23503"
	restrictViolation   = "23001"
)

// InTx runs fn in a transaction and rolls back on any error. A rollback after a successful
// commit is a no-op, which is why the deferred call needs no bookkeeping.
func InTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
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

func SQLState(err error) string {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState()
	}
	return ""
}

func IsNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func IsUniqueViolation(err error) bool { return SQLState(err) == uniqueViolation }

func IsForeignKeyViolation(err error) bool { return SQLState(err) == foreignKeyViolation }

// IsRestrictViolation is ON DELETE RESTRICT refusing to orphan a referencing row, or a
// foreign key naming a parent that does not exist — a caller cannot act differently on the
// two, and both mean "the thing you named is not there or not free".
func IsRestrictViolation(err error) bool {
	state := SQLState(err)
	return state == restrictViolation || state == foreignKeyViolation
}

// JSONObject keeps a JSONB column at '{}' instead of the JSON literal null, so a reader never
// has to tell the two apart. Anything else is handed to pgx as-is and marshalled by its JSON
// codec.
//
// The map case is spelled out because a nil map boxed in an interface is *not* == nil — that is
// what silently turned an unset column into a NOT NULL violation.
func JSONObject(v any) any {
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

// Int64Array keeps a NOT NULL BIGINT[] column at '{}': a nil Go slice encodes as SQL NULL,
// which is the same trap JSONObject covers for a nil map.
func Int64Array(v []int64) []int64 {
	if v == nil {
		return []int64{}
	}
	return v
}

// NullTime is for a bound the query itself treats as optional — the SQL reads
// `@before::timestamptz IS NULL OR ...`, so a zero time has to arrive as NULL.
func NullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// NullID is the same for a key: a zero id is not a row anybody has, so it is NULL rather than 0.
func NullID(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

// NullJSON keeps an *optional* JSONB column NULL when there is nothing to store, which is the
// opposite of JSONObject's NOT NULL columns.
func NullJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

// Querier is what a pool and a transaction have in common. An audit row is written inside the
// transaction that changed the record; a standalone insert has no transaction to join.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// InsertAuditLog derives the record's next version inside the same transaction as the change,
// so several facts from one Save get 1,2,3 without colliding on the unique key.
//
// Every parameter is cast: @table_name and @record_id appear in both the SELECT list and the
// WHERE of an INSERT ... SELECT, and without the casts Postgres deduces two types for one
// placeholder and refuses the statement (42P08).
func InsertAuditLog(ctx context.Context, q Querier, e common.AuditEntry) error {
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
		"diff":        JSONObject(e.Diff),
		"snapshot":    JSONObject(e.Snapshot),
	}
	if _, err := q.Exec(ctx, stmt, args); err != nil {
		return fmt.Errorf("db insert audit log: %w", err)
	}
	return nil
}
