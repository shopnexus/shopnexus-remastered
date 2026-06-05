package accountbiz

import (
	"fmt"

	restate "github.com/restatedev/sdk-go"

	accountdb "shopnexus-server/internal/module/account/db/sqlc"

	"github.com/google/uuid"
)

// SuspendAccountParams holds the parameters for suspending an account.
type SuspendAccountParams struct {
	AccountID uuid.UUID
}

// SuspendAccount suspends the account with the given ID.
func (b *AccountHandler) SuspendAccount(ctx restate.Context, params SuspendAccountParams) error {
	if _, err := b.storage.Querier().UpdateAccount(ctx, accountdb.UpdateAccountParams{
		ID:     params.AccountID,
		Status: accountdb.NullAccountStatus{AccountStatus: accountdb.AccountStatusSuspended, Valid: true},
	}); err != nil {
		return fmt.Errorf("db suspend account: %w", err)
	}
	return nil
}
