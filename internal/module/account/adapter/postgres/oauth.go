package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/account/domain"
)

const oauthColumns = `id, account_id, provider, provider_uid, created_at`

// listIdentities is a child read of the aggregate. The unique pairs the writes rely on —
// (provider, provider_uid) and (account_id, provider) — are enforced by the schema.
func listIdentities(ctx context.Context, qr querier, accountID int64) ([]*domain.OAuthIdentity, error) {
	q := `SELECT ` + oauthColumns + `
	      FROM oauth_identity WHERE account_id = @account_id ORDER BY created_at`
	rows, err := qr.Query(ctx, q, pgx.NamedArgs{"account_id": accountID})
	if err != nil {
		return nil, fmt.Errorf("db query oauth identities: %w", err)
	}
	defer rows.Close()

	var out []*domain.OAuthIdentity
	for rows.Next() {
		var i domain.OAuthIdentity
		if err := rows.Scan(&i.ID, &i.AccountID, &i.Provider, &i.ProviderUID, &i.CreatedAt); err != nil {
			return nil, fmt.Errorf("db scan oauth identity row: %w", err)
		}
		out = append(out, &i)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate oauth identities: %w", err)
	}
	return out, nil
}
