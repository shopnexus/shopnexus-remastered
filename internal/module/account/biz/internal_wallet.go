package accountbiz

import (
	"fmt"
	accountdb "shopnexus-server/internal/module/account/db/sqlc"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
	restate "github.com/restatedev/sdk-go"
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
func (b *AccountHandler) GetWalletBalance(ctx restate.Context, accountID uuid.UUID) (int64, error) {
	balance, err := restate.Run(ctx, func(rctx restate.RunContext) (int64, error) {
		return b.storage.Querier().GetInternalBalance(rctx, accountID)
	})
	if err != nil {
		return 0, fmt.Errorf("get internal balance: %w", err)
	}
	return balance, nil
}

// WalletDebit deducts min(balance, amount) atomically and returns (deducted, new balance).
// The underlying CTE row-locks the profile so concurrent debits serialize correctly.
func (b *AccountHandler) WalletDebit(ctx restate.Context, params WalletDebitParams) (WalletDebitResult, error) {
	if err := validator.Validate(params); err != nil {
		return WalletDebitResult{}, fmt.Errorf("debit internal balance: %w", err)
	}
	res, err := restate.Run(ctx, func(rctx restate.RunContext) (WalletDebitResult, error) {
		row, err := b.storage.Querier().DebitInternalBalance(rctx, accountdb.DebitInternalBalanceParams{
			AccountID: params.AccountID,
			Amount:    params.Amount,
		})
		if err != nil {
			return WalletDebitResult{}, err
		}
		return WalletDebitResult{Deducted: row.OldBalance - row.NewBalance, Balance: row.NewBalance}, nil
	})
	if err != nil {
		return WalletDebitResult{}, fmt.Errorf("debit internal balance: %w", err)
	}
	return res, nil
}

// WalletCredit adds the given amount to the account's internal balance.
func (b *AccountHandler) WalletCredit(ctx restate.Context, params WalletCreditParams) error {
	if err := validator.Validate(params); err != nil {
		return fmt.Errorf("credit internal balance: %w", err)
	}
	if err := restate.RunVoid(ctx, func(rctx restate.RunContext) error {
		_, err := b.storage.Querier().CreditInternalBalance(rctx, accountdb.CreditInternalBalanceParams{
			AccountID: params.AccountID,
			Amount:    params.Amount,
		})
		return err
	}); err != nil {
		return fmt.Errorf("credit internal balance: %w", err)
	}
	return nil
}
