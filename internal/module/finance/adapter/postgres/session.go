package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/common/dbx"
	"shopnexus/internal/module/finance/domain"
	"shopnexus/internal/module/finance/port"
)

const sessionColumns = `id, kind::text, status::text, from_id, to_id, note, currency,
	       total_amount, fx_snapshot, data, created_at, paid_at, expired_at`

func scanSession(row pgx.Row) (domain.Session, error) {
	var s domain.Session
	err := row.Scan(&s.ID, &s.Kind, &s.Status, &nullableInt64{&s.FromID}, &nullableInt64{&s.ToID},
		&s.Note, &s.Currency, &s.TotalAmount, &s.FXSnapshot, &s.Data, &s.CreatedAt,
		&s.PaidAt, &s.ExpiredAt)
	if dbx.IsNoRows(err) {
		return domain.Session{}, domain.ErrSessionNotFound
	}
	if err != nil {
		return domain.Session{}, fmt.Errorf("db scan payment session: %w", err)
	}
	return s, nil
}

// SaveSession writes the status, the settlement time and the data. The WHERE clause
// carries the transition: a session moves from the status the caller read, so two
// concurrent settlements cannot both land and there is no version column to keep.
func (r *Repo) SaveSession(ctx context.Context, s domain.Session) error {
	const q = `UPDATE payment_session
	           SET status = @status, paid_at = @paid_at, data = @data, note = @note
	           WHERE id = @id`
	args := pgx.NamedArgs{
		"id": s.ID, "status": s.Status, "paid_at": s.PaidAt,
		"data": dbx.JSONObject(rawJSON(s.Data)), "note": s.Note,
	}
	tag, err := r.pool.Exec(ctx, q, args)
	if err != nil {
		return fmt.Errorf("db update payment session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrSessionNotFound
	}
	return nil
}

// ListSessions pages a party's sessions, or every session when AccountID is zero —
// which is the admin view, and the only caller allowed to pass it.
func (r *Repo) ListSessions(ctx context.Context, f port.SessionFilter) ([]domain.Session, int64, error) {
	const q = `SELECT ` + sessionColumns + `, COUNT(*) OVER () AS total_count
	           FROM payment_session
	           WHERE (@account_id = 0 OR from_id = @account_id OR to_id = @account_id)
	             AND (@kind::text IS NULL OR kind::text = @kind::text)
	             AND (@status::text IS NULL OR status::text = @status::text)
	           ORDER BY created_at DESC, id DESC
	           LIMIT @limit OFFSET @offset`
	args := pgx.NamedArgs{
		"account_id": f.AccountID,
		"kind":       nullString(f.Kind),
		"status":     nullString(f.Status),
		"limit":      f.Limit,
		"offset":     f.Offset,
	}
	rows, err := r.pool.Query(ctx, q, args)
	if err != nil {
		return nil, 0, fmt.Errorf("db query payment sessions: %w", err)
	}
	defer rows.Close()

	var (
		out   []domain.Session
		total int64
	)
	for rows.Next() {
		var s domain.Session
		if err := rows.Scan(&s.ID, &s.Kind, &s.Status, &nullableInt64{&s.FromID},
			&nullableInt64{&s.ToID}, &s.Note, &s.Currency, &s.TotalAmount, &s.FXSnapshot,
			&s.Data, &s.CreatedAt, &s.PaidAt, &s.ExpiredAt, &total); err != nil {
			return nil, 0, fmt.Errorf("db scan payment session row: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("db iterate payment sessions: %w", err)
	}
	return out, total, nil
}

func (r *Repo) NextTransactionID(ctx context.Context) (int64, error) {
	const q = `SELECT nextval(pg_get_serial_sequence('transaction', 'id'))`
	var n int64
	if err := r.pool.QueryRow(ctx, q).Scan(&n); err != nil {
		return 0, fmt.Errorf("db next transaction id: %w", err)
	}
	return n, nil
}

const transactionColumns = `id, session_id, status::text, note, error, payment_option,
	       provider_ref, data, amount, currency, reverses_id, created_at, settled_at, expired_at`

func scanTransaction(row pgx.Row) (domain.Transaction, error) {
	var t domain.Transaction
	err := row.Scan(&t.ID, &t.SessionID, &t.Status, &t.Note, &t.Error, &t.PaymentOption,
		&t.ProviderRef, &t.Data, &t.Amount, &t.Currency, &t.ReversesID, &t.CreatedAt,
		&t.SettledAt, &t.ExpiredAt)
	if dbx.IsNoRows(err) {
		return domain.Transaction{}, domain.ErrTransactionNotFound
	}
	if err != nil {
		return domain.Transaction{}, fmt.Errorf("db scan transaction: %w", err)
	}
	return t, nil
}

func (r *Repo) InsertTransaction(ctx context.Context, t *domain.Transaction) error {
	const q = `INSERT INTO transaction
	             (id, session_id, status, note, error, payment_option, provider_ref, data,
	              amount, currency, reverses_id, expired_at)
	           VALUES (@id, @session_id, @status, @note, @error, @payment_option, @provider_ref,
	                   @data, @amount, @currency, @reverses_id, @expired_at)
	           RETURNING created_at`
	args := pgx.NamedArgs{
		"id": t.ID, "session_id": t.SessionID, "status": t.Status, "note": t.Note,
		"error": t.Error, "payment_option": t.PaymentOption, "provider_ref": t.ProviderRef,
		"data": dbx.JSONObject(rawJSON(t.Data)), "amount": t.Amount, "currency": t.Currency,
		"reverses_id": t.ReversesID, "expired_at": t.ExpiredAt,
	}
	if err := r.pool.QueryRow(ctx, q, args).Scan(&t.CreatedAt); err != nil {
		if dbx.IsUniqueViolation(err) {
			return domain.ErrMovementAlreadyPosted
		}
		return fmt.Errorf("db insert transaction: %w", err)
	}
	return nil
}

// SaveTransaction settles a leg. `WHERE status = 'pending'` is the transition: a
// webhook delivered twice finds nothing to update, which is what stops a redelivery
// from being booked as a second settlement.
func (r *Repo) SaveTransaction(ctx context.Context, t domain.Transaction) error {
	const q = `UPDATE transaction
	           SET status = @status, provider_ref = @provider_ref, error = @error,
	               settled_at = @settled_at, data = @data
	           WHERE id = @id AND status = 'pending'`
	args := pgx.NamedArgs{
		"id": t.ID, "status": t.Status, "provider_ref": t.ProviderRef, "error": t.Error,
		"settled_at": t.SettledAt, "data": dbx.JSONObject(rawJSON(t.Data)),
	}
	tag, err := r.pool.Exec(ctx, q, args)
	if err != nil {
		return fmt.Errorf("db update transaction: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrTransactionSettled
	}
	return nil
}

func (r *Repo) ListTransactions(ctx context.Context, sessionID int64) ([]domain.Transaction, error) {
	const q = `SELECT ` + transactionColumns + ` FROM transaction
	           WHERE session_id = @session_id ORDER BY id`
	rows, err := r.pool.Query(ctx, q, pgx.NamedArgs{"session_id": sessionID})
	if err != nil {
		return nil, fmt.Errorf("db query transactions: %w", err)
	}
	defer rows.Close()

	var out []domain.Transaction
	for rows.Next() {
		t, err := scanTransaction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate transactions: %w", err)
	}
	return out, nil
}

func (r *Repo) FindTransactionByID(ctx context.Context, id int64) (domain.Transaction, error) {
	const q = `SELECT ` + transactionColumns + ` FROM transaction WHERE id = @id`
	return scanTransaction(r.pool.QueryRow(ctx, q, pgx.NamedArgs{"id": id}))
}

func (r *Repo) FindTransactionByProviderRef(ctx context.Context, ref string) (domain.Transaction, error) {
	const q = `SELECT ` + transactionColumns + ` FROM transaction WHERE provider_ref = @ref`
	return scanTransaction(r.pool.QueryRow(ctx, q, pgx.NamedArgs{"ref": ref}))
}

// nullableInt64 scans a nullable key into a plain int64, where zero means NULL. The
// domain uses zero for "system" on a session's two sides, so this keeps the entity
// free of pointers that would only ever be compared against nil.
type nullableInt64 struct{ dst *int64 }

func (n *nullableInt64) ScanNull() error {
	*n.dst = 0
	return nil
}

func (n *nullableInt64) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*n.dst = 0
	case int64:
		*n.dst = v
	default:
		return fmt.Errorf("scan nullable int64: unexpected %T", src)
	}
	return nil
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// rawJSON keeps a JSONB column at '{}' when the entity carries nothing, and hands
// the bytes to pgx as-is otherwise.
func rawJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}
