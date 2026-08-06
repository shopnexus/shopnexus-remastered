package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/module/account/port"
	"shopnexus/internal/module/common"
	"shopnexus/internal/module/common/dbx"
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
	               WHERE account_id = @account_id AND status::text = @status
	                 AND (expires_at IS NULL OR expires_at > now())
	           )`
	var ok bool
	args := pgx.NamedArgs{"account_id": accountID, "status": string(domain.IdentityVerified)}
	if err := r.pool.QueryRow(ctx, q, args).Scan(&ok); err != nil {
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
	           WHERE account_id = ANY(@ids) AND status::text = @status
	             AND (expires_at IS NULL OR expires_at > now())`
	args := pgx.NamedArgs{"ids": accountIDs, "status": string(domain.IdentityVerified)}
	rows, err := r.pool.Query(ctx, q, args)
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

// IsFollowing answers whether one account follows another. A separate read rather than a
// column on the follower count: the count is a fact about the followee, this is a fact about
// the pair, and only a signed-in reader has the second one.
func (r *Repo) IsFollowing(ctx context.Context, followerID, followeeID int64) (bool, error) {
	const q = `SELECT EXISTS (
	             SELECT 1 FROM follow WHERE follower_id = @follower AND followee_id = @followee)`
	var yes bool
	if err := r.pool.QueryRow(ctx, q, pgx.NamedArgs{
		"follower": followerID, "followee": followeeID,
	}).Scan(&yes); err != nil {
		return false, fmt.Errorf("db check follow: %w", err)
	}
	return yes, nil
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
func (r *Repo) InsertAuditLog(ctx context.Context, e common.AuditEntry) error {
	return dbx.InsertAuditLog(ctx, r.pool, e)
}

// insertAuditLog appends the next version for that record in one statement: the version
// is derived from the rows already there, so two concurrent writers collide on the UNIQUE
// (table_name, record_id, version) instead of silently overwriting a history entry.
//
