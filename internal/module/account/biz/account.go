package accountbiz

import (
	"context"
	"fmt"

	accountdb "shopnexus-server/internal/module/account/db/sqlc"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
)

// SuspendAccountParams holds the parameters for suspending an account.
type SuspendAccountParams struct {
	AccountID uuid.UUID
}

// SuspendAccount suspends the account with the given ID.
func (b *AccountHandler) SuspendAccount(ctx context.Context, params SuspendAccountParams) error {
	if err := validator.Validate(params); err != nil {
		return fmt.Errorf("validate suspend account params: %w", err)
	}
	if _, err := b.storage.Querier().UpdateAccount(ctx, accountdb.UpdateAccountParams{
		ID:     params.AccountID,
		Status: accountdb.NullAccountStatus{AccountStatus: accountdb.AccountStatusSuspended, Valid: true},
	}); err != nil {
		return fmt.Errorf("db suspend account: %w", err)
	}
	return nil
}
