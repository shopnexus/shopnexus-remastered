package accountbiz

import (
	"context"
	"fmt"
	accountdb "shopnexus-server/internal/module/account/db/sqlc"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
)

type WalletDebitParams struct {
	AccountID uuid.UUID `json:"account_id" validate:"required"`
	Amount    int64     `json:"amount"     validate:"required,gt=0"`
	Reference string    `json:"reference"`
	Note      string    `json:"note"`
}

type WalletDebitResult struct {
	Deducted int64 `json:"deducted"`
	Balance  int64 `json:"balance"`
}

type WalletCreditParams struct {
	AccountID uuid.UUID `json:"account_id" validate:"required"`
	Amount    int64     `json:"amount"     validate:"required,gt=0"`
	Type      string    `json:"type"       validate:"required"`
	Reference string    `json:"reference"`
	Note      string    `json:"note"`
}

// GetWalletBalance returns the account's internal money balance.
func (b *AccountHandler) GetWalletBalance(ctx context.Context, accountID uuid.UUID) (int64, error) {
	balance, err := b.storage.Querier().GetInternalBalance(ctx, accountID)
	if err != nil {
		return 0, fmt.Errorf("get internal balance: %w", err)
	}
	return balance, nil
}

// WalletDebit deducts min(balance, amount) atomically and returns (deducted, new balance).
// The underlying CTE row-locks the profile so concurrent debits serialize correctly.
func (b *AccountHandler) WalletDebit(ctx context.Context, params WalletDebitParams) (WalletDebitResult, error) {
	if err := validator.Validate(params); err != nil {
		return WalletDebitResult{}, fmt.Errorf("debit internal balance: %w", err)
	}
	res, err := func() (WalletDebitResult, error) {
		row, err := b.storage.Querier().DebitInternalBalance(ctx, accountdb.DebitInternalBalanceParams{
			AccountID: params.AccountID,
			Amount:    params.Amount,
		})
		if err != nil {
			return WalletDebitResult{}, err
		}
		return WalletDebitResult{Deducted: row.OldBalance - row.NewBalance, Balance: row.NewBalance}, nil
	}()
	if err != nil {
		return WalletDebitResult{}, fmt.Errorf("debit internal balance: %w", err)
	}
	return res, nil
}

// WalletCredit adds the given amount to the account's internal balance.
func (b *AccountHandler) WalletCredit(ctx context.Context, params WalletCreditParams) error {
	if err := validator.Validate(params); err != nil {
		return fmt.Errorf("credit internal balance: %w", err)
	}
	if _, err := b.storage.Querier().CreditInternalBalance(ctx, accountdb.CreditInternalBalanceParams{
		AccountID: params.AccountID,
		Amount:    params.Amount,
	}); err != nil {
		return fmt.Errorf("credit internal balance: %w", err)
	}
	return nil
}
