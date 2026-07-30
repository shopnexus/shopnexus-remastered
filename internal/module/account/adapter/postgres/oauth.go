package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/account/domain"
)

const oauthColumns = `id, account_id, provider, provider_uid, created_at`

func (r *Repo) FindOAuthIdentity(ctx context.Context, provider, providerUID string) (domain.OAuthIdentity, error) {
	q := `SELECT ` + oauthColumns + `
	      FROM oauth_identity WHERE provider = @provider AND provider_uid = @provider_uid`
	var i domain.OAuthIdentity
	err := r.pool.QueryRow(ctx, q, pgx.NamedArgs{"provider": provider, "provider_uid": providerUID}).
		Scan(&i.ID, &i.AccountID, &i.Provider, &i.ProviderUID, &i.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.OAuthIdentity{}, domain.ErrOAuthIdentityNotFound
	}
	if err != nil {
		return domain.OAuthIdentity{}, fmt.Errorf("db scan oauth identity: %w", err)
	}
	return i, nil
}

// InsertOAuthIdentity links a provider account to a local one. The unique pair
// (provider, provider_uid) is what stops one provider account from being linked to two
// local accounts, and (account_id, provider) what keeps one identity per provider.
func (r *Repo) InsertOAuthIdentity(ctx context.Context, i *domain.OAuthIdentity) error {
	const q = `INSERT INTO oauth_identity (account_id, provider, provider_uid)
	           VALUES (@account_id, @provider, @provider_uid)
	           RETURNING id, created_at`
	args := pgx.NamedArgs{"account_id": i.AccountID, "provider": i.Provider, "provider_uid": i.ProviderUID}
	if err := r.pool.QueryRow(ctx, q, args).Scan(&i.ID, &i.CreatedAt); err != nil {
		if isUniqueViolation(err) {
			return domain.ErrIdentifierTaken
		}
		return fmt.Errorf("db insert oauth identity: %w", err)
	}
	return nil
}

func (r *Repo) ListOAuthIdentities(ctx context.Context, accountID int64) ([]domain.OAuthIdentity, error) {
	q := `SELECT ` + oauthColumns + `
	      FROM oauth_identity WHERE account_id = @account_id ORDER BY created_at`
	rows, err := r.pool.Query(ctx, q, pgx.NamedArgs{"account_id": accountID})
	if err != nil {
		return nil, fmt.Errorf("db query oauth identities: %w", err)
	}
	defer rows.Close()

	var out []domain.OAuthIdentity
	for rows.Next() {
		var i domain.OAuthIdentity
		if err := rows.Scan(&i.ID, &i.AccountID, &i.Provider, &i.ProviderUID, &i.CreatedAt); err != nil {
			return nil, fmt.Errorf("db scan oauth identity row: %w", err)
		}
		out = append(out, i)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate oauth identities: %w", err)
	}
	return out, nil
}

func (r *Repo) DeleteOAuthIdentity(ctx context.Context, accountID int64, provider string) error {
	const q = `DELETE FROM oauth_identity WHERE account_id = @account_id AND provider = @provider`
	tag, err := r.pool.Exec(ctx, q, pgx.NamedArgs{"account_id": accountID, "provider": provider})
	if err != nil {
		return fmt.Errorf("db delete oauth identity: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrOAuthIdentityNotFound
	}
	return nil
}

// CountOAuthIdentities is what "would unlinking leave no way to sign in?" is decided
// on, together with whether the account has a password.
func (r *Repo) CountOAuthIdentities(ctx context.Context, accountID int64) (int64, error) {
	const q = `SELECT COUNT(*) FROM oauth_identity WHERE account_id = @account_id`
	var n int64
	if err := r.pool.QueryRow(ctx, q, pgx.NamedArgs{"account_id": accountID}).Scan(&n); err != nil {
		return 0, fmt.Errorf("db count oauth identities: %w", err)
	}
	return n, nil
}
