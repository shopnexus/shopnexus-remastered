// Package postgres implements the account port.Repository with pgx named args and
// hand-written SQL.
//
// All SQL is unqualified: the pool sets search_path to this module's schema, so a
// table name is enough and the module can later move to its own database without a
// rewrite.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/module/account/port"
)

// The Postgres error codes the schema turns business rules into, so the adapter maps
// them back to the domain's own errors instead of leaking a driver error.
const (
	uniqueViolation     = "23505"
	foreignKeyViolation = "23503"
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

// nullText keeps an optional column NULL rather than storing an empty string, which
// is what makes the UNIQUE constraints on the identifiers work: Postgres allows many
// NULLs and exactly one ”.
func nullText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullID does the same for an optional key. The zero id means "no id", and identity
// columns never produce 0.
func nullID(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

// jsonObject keeps a JSONB column at '{}' instead of the JSON literal null, so a
// reader never has to tell the two apart.
func jsonObject(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// nullTime is for a bound the query itself treats as optional — the SQL reads
// `@before::timestamptz IS NULL OR ...`, so a zero time has to arrive as NULL.
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func sqlState(err error) string {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState()
	}
	return ""
}

func isUniqueViolation(err error) bool { return sqlState(err) == uniqueViolation }

func isForeignKeyViolation(err error) bool { return sqlState(err) == foreignKeyViolation }

// accountColumns is shared by every read of a whole account. COALESCE turns the
// nullable identifier columns into the empty string the domain uses for "not set",
// and every enum column is cast to text: the domain's own types are strings, and an
// explicit cast keeps the scan independent of whether the driver knows the enum's OID.
const accountColumns = `id, status::text, role::text, COALESCE(phone, ''), COALESCE(email, ''), COALESCE(username, ''),
	       COALESCE(password, ''), email_verified, created_at, suspended_until, COALESCE(suspension_reason, '')`

// scanAccount reads accountColumns, in that order.
func scanAccount(row pgx.Row) (domain.Account, error) {
	var a domain.Account
	err := row.Scan(&a.ID, &a.Status, &a.Role, &a.Phone, &a.Email, &a.Username,
		&a.PasswordHash, &a.EmailVerified, &a.CreatedAt, &a.SuspendedUntil, &a.SuspensionReason)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Account{}, domain.ErrAccountNotFound
	}
	if err != nil {
		return domain.Account{}, fmt.Errorf("db scan account: %w", err)
	}
	return a, nil
}

// profileColumns is shared by the profile reads.
const profileColumns = `id, name, COALESCE(description, ''), COALESCE(gender::text, ''), date_of_birth,
	       COALESCE(avatar_resource_id, 0), country, locale, timezone, created_at`

func scanProfile(row pgx.Row) (domain.Profile, error) {
	var p domain.Profile
	err := row.Scan(&p.ID, &p.Name, &p.Description, &p.Gender, &p.DateOfBirth,
		&p.AvatarResourceID, &p.Country, &p.Locale, &p.Timezone, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// A profile is created with its account in one transaction, so a missing one
		// means a missing account rather than a half-built row.
		return domain.Profile{}, domain.ErrAccountNotFound
	}
	if err != nil {
		return domain.Profile{}, fmt.Errorf("db scan profile: %w", err)
	}
	return p, nil
}
