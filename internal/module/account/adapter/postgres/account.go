package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/module/account/port"
)

// SearchAccounts is the moderator's search. One statement covers both shapes of
// query: an exact hit on any identifier (a key lookup, which is how a moderator
// arrives here from a report) or a fragment of the display name (served by the
// trigram index on account.name).
//
// It answers flat summary rows rather than aggregates: a page of twenty accounts must
// not be twenty aggregate loads, and nothing on this screen is editable.
//
// COUNT(*) OVER () brings the total back with the rows, so a page costs one round
// trip rather than a query plus a matching count that can drift from it.
func (r *Repo) SearchAccounts(ctx context.Context, f port.AccountFilter) ([]port.AccountSummary, int64, error) {
	const q = `SELECT id, status::text, role::text, phone, email, username,
	                  email_verified, suspended_until, suspension_reason, created_at,
	                  name, COUNT(*) OVER () AS total_count
	           FROM account
	           WHERE (@query = '' OR email = @query OR phone = @query OR username = @query
	                  OR name ILIKE '%' || @query || '%')
	             AND (@status = '' OR status::text = @status)
	             AND (@role = '' OR role::text = @role)
	           ORDER BY created_at DESC, id DESC
	           LIMIT @limit OFFSET @offset`
	args := pgx.NamedArgs{
		"query":  f.Query,
		"status": string(f.Status),
		"role":   string(f.Role),
		"limit":  f.Limit,
		"offset": f.Offset,
	}
	rows, err := r.pool.Query(ctx, q, args)
	if err != nil {
		return nil, 0, fmt.Errorf("db query accounts: %w", err)
	}
	defer rows.Close()

	var (
		out   []port.AccountSummary
		total int64
	)
	for rows.Next() {
		var s port.AccountSummary
		if err := rows.Scan(&s.ID, &s.Status, &s.Role, &s.Phone, &s.Email, &s.Username,
			&s.EmailVerified, &s.SuspendedUntil, &s.SuspensionReason, &s.CreatedAt,
			&s.Name, &total); err != nil {
			return nil, 0, fmt.Errorf("db scan account row: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("db iterate accounts: %w", err)
	}
	return out, total, nil
}

// --- profile ---

func (r *Repo) FindProfile(ctx context.Context, accountID int64) (domain.Profile, error) {
	q := `SELECT ` + profileColumns + ` FROM account WHERE id = @id`
	return scanProfile(r.pool.QueryRow(ctx, q, pgx.NamedArgs{"id": accountID}))
}

// FindProfiles resolves a batch, so a page of twenty followers costs one query rather
// than twenty.
func (r *Repo) FindProfiles(ctx context.Context, accountIDs []int64) (map[int64]domain.Profile, error) {
	out := make(map[int64]domain.Profile, len(accountIDs))
	if len(accountIDs) == 0 {
		return out, nil
	}
	q := `SELECT ` + profileColumns + ` FROM account WHERE id = ANY(@ids)`
	rows, err := r.pool.Query(ctx, q, pgx.NamedArgs{"ids": accountIDs})
	if err != nil {
		return nil, fmt.Errorf("db query profiles: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		out[p.ID] = p
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate profiles: %w", err)
	}
	return out, nil
}

func (r *Repo) HasLiveVerifiedDocument(ctx context.Context, accountID int64) (bool, error) {
	const q = `SELECT EXISTS (
	               SELECT 1 FROM identity_document
	               WHERE account_id = @account_id AND status = 'verified'
	                 AND (expires_at IS NULL OR expires_at > now())
	           )`
	var ok bool
	if err := r.pool.QueryRow(ctx, q, pgx.NamedArgs{"account_id": accountID}).Scan(&ok); err != nil {
		return false, fmt.Errorf("db query live verified document: %w", err)
	}
	return ok, nil
}

func (r *Repo) LiveVerifiedDocuments(ctx context.Context, accountIDs []int64) (map[int64]bool, error) {
	out := make(map[int64]bool, len(accountIDs))
	if len(accountIDs) == 0 {
		return out, nil
	}
	const q = `SELECT account_id FROM identity_document
	           WHERE account_id = ANY(@ids) AND status = 'verified'
	             AND (expires_at IS NULL OR expires_at > now())`
	rows, err := r.pool.Query(ctx, q, pgx.NamedArgs{"ids": accountIDs})
	if err != nil {
		return nil, fmt.Errorf("db query live verified documents: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("db scan live verified document row: %w", err)
		}
		out[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate live verified documents: %w", err)
	}
	return out, nil
}

func (r *Repo) CountFollowers(ctx context.Context, accountID int64) (int64, error) {
	const q = `SELECT COUNT(*) FROM follow WHERE followee_id = @account_id`
	var n int64
	if err := r.pool.QueryRow(ctx, q, pgx.NamedArgs{"account_id": accountID}).Scan(&n); err != nil {
		return 0, fmt.Errorf("db count followers: %w", err)
	}
	return n, nil
}

// --- audit ---

// InsertAuditLog is the standalone path: an insert, or a table that is not the account
// aggregate. An update to the aggregate has its trail written by Save, in the same
// transaction as the change it describes.
func (r *Repo) InsertAuditLog(ctx context.Context, e port.AuditEntry) error {
	return insertAuditLog(ctx, r.pool, e)
}

// insertAuditLog appends the next version for that record in one statement: the version
// is derived from the rows already there, so two concurrent writers collide on the UNIQUE
// (table_name, record_id, version) instead of silently overwriting a history entry.
//
// Every parameter in the SELECT list is cast: an INSERT ... SELECT does not give the
// planner the target column's type, so table_name and record_id — which appear in the
// WHERE too — would otherwise be deduced twice and disagree (42P08).
func insertAuditLog(ctx context.Context, q querier, e port.AuditEntry) error {
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
	if _, err := q.Exec(ctx, stmt, args); err != nil {
		return fmt.Errorf("db insert audit log: %w", err)
	}
	return nil
}
