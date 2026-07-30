package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/account/domain"
)

const identityColumns = `id, account_id, doc_type::text, provider, provider_ref, status::text,
	       COALESCE(rejection_reason, ''), verified_at, expires_at, created_at`

func scanIdentity(row pgx.Row) (domain.IdentityDocument, error) {
	var d domain.IdentityDocument
	err := row.Scan(&d.ID, &d.AccountID, &d.DocType, &d.Provider, &d.ProviderRef, &d.Status,
		&d.RejectionReason, &d.VerifiedAt, &d.ExpiresAt, &d.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.IdentityDocument{}, domain.ErrIdentityDocumentNotFound
	}
	if err != nil {
		return domain.IdentityDocument{}, fmt.Errorf("db scan identity document: %w", err)
	}
	return d, nil
}

// InsertIdentityDocument writes the case with whatever verdict it already carries: a
// vendor that reads the scans itself answers before the row exists, so "insert pending,
// then decide" would store a state that never happened and briefly show the account an
// undecided case.
func (r *Repo) InsertIdentityDocument(ctx context.Context, d *domain.IdentityDocument) error {
	const q = `INSERT INTO identity_document (account_id, doc_type, provider, provider_ref, status,
	                                  rejection_reason, verified_at, expires_at)
	           VALUES (@account_id, @doc_type, @provider, @provider_ref, @status,
	                   @rejection_reason, @verified_at, @expires_at)
	           RETURNING id, created_at`
	args := pgx.NamedArgs{
		"account_id":       d.AccountID,
		"doc_type":         string(d.DocType),
		"provider":         d.Provider,
		"provider_ref":     d.ProviderRef,
		"status":           string(d.Status),
		"rejection_reason": nullText(d.RejectionReason),
		"verified_at":      d.VerifiedAt,
		"expires_at":       d.ExpiresAt,
	}
	if err := r.pool.QueryRow(ctx, q, args).Scan(&d.ID, &d.CreatedAt); err != nil {
		switch {
		case isForeignKeyViolation(err):
			return domain.ErrAccountNotFound
		case isUniqueViolation(err):
			// The partial unique index on verified rows: another check landed first.
			return domain.ErrIdentityAlreadyVerified
		}
		return fmt.Errorf("db insert identity document: %w", err)
	}
	return nil
}

func (r *Repo) FindIdentityDocument(ctx context.Context, id int64) (domain.IdentityDocument, error) {
	q := `SELECT ` + identityColumns + ` FROM identity_document WHERE id = @id`
	return scanIdentity(r.pool.QueryRow(ctx, q, pgx.NamedArgs{"id": id}))
}

// ListIdentityDocuments is the account's own history, newest first: a rejected check
// followed by a fresh one is the normal case, and the client shows both.
func (r *Repo) ListIdentityDocuments(ctx context.Context, accountID int64) ([]domain.IdentityDocument, error) {
	q := `SELECT ` + identityColumns + `
	      FROM identity_document WHERE account_id = @account_id ORDER BY created_at DESC`
	rows, err := r.pool.Query(ctx, q, pgx.NamedArgs{"account_id": accountID})
	if err != nil {
		return nil, fmt.Errorf("db query identity documents: %w", err)
	}
	defer rows.Close()

	out, err := collectIdentities(rows)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListIdentityDocumentsByStatus is the review queue, oldest first: a queue is worked
// from the front, and the partial index on pending rows is ordered by created_at.
func (r *Repo) ListIdentityDocumentsByStatus(ctx context.Context, status domain.IdentityStatus, offset, limit int) ([]domain.IdentityDocument, int64, error) {
	q := `SELECT ` + identityColumns + `, COUNT(*) OVER () AS total_count
	      FROM identity_document
	      WHERE status::text = @status
	      ORDER BY created_at
	      LIMIT @limit OFFSET @offset`
	args := pgx.NamedArgs{"status": string(status), "limit": limit, "offset": offset}
	rows, err := r.pool.Query(ctx, q, args)
	if err != nil {
		return nil, 0, fmt.Errorf("db query identity document queue: %w", err)
	}
	defer rows.Close()

	var (
		out   []domain.IdentityDocument
		total int64
	)
	for rows.Next() {
		var d domain.IdentityDocument
		if err := rows.Scan(&d.ID, &d.AccountID, &d.DocType, &d.Provider, &d.ProviderRef, &d.Status,
			&d.RejectionReason, &d.VerifiedAt, &d.ExpiresAt, &d.CreatedAt, &total); err != nil {
			return nil, 0, fmt.Errorf("db scan identity document queue row: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("db iterate identity document queue: %w", err)
	}
	return out, total, nil
}

// UpdateIdentityVerdict only moves a pending row, so two moderators deciding at once
// cannot overwrite each other: the second one affects no rows and is told the document
// was already decided.
//
// The unique index allowing one verified document per account is what turns "this
// account is already verified" into a conflict rather than a second live document.
func (r *Repo) UpdateIdentityVerdict(ctx context.Context, d domain.IdentityDocument) error {
	const q = `UPDATE identity_document
	           SET status = @status, rejection_reason = @rejection_reason,
	               verified_at = @verified_at, expires_at = @expires_at
	           WHERE id = @id AND status = 'pending'`
	args := pgx.NamedArgs{
		"id":               d.ID,
		"status":           string(d.Status),
		"rejection_reason": nullText(d.RejectionReason),
		"verified_at":      d.VerifiedAt,
		"expires_at":       d.ExpiresAt,
	}
	tag, err := r.pool.Exec(ctx, q, args)
	if err != nil {
		if isUniqueViolation(err) {
			return domain.ErrIdentityAlreadyVerified
		}
		return fmt.Errorf("db update identity document verdict: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrIdentityAlreadyDecided
	}
	return nil
}

func collectIdentities(rows pgx.Rows) ([]domain.IdentityDocument, error) {
	var out []domain.IdentityDocument
	for rows.Next() {
		var d domain.IdentityDocument
		if err := rows.Scan(&d.ID, &d.AccountID, &d.DocType, &d.Provider, &d.ProviderRef, &d.Status,
			&d.RejectionReason, &d.VerifiedAt, &d.ExpiresAt, &d.CreatedAt); err != nil {
			return nil, fmt.Errorf("db scan identity document row: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate identity documents: %w", err)
	}
	return out, nil
}
