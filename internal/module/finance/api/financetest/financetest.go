// Package financetest provides a stub financeapi.Service for tests.
//
// A test that cares about one method should not have to write the rest. Embed Stub and
// override what the test is about; anything left over answers 501, so an unstubbed call
// shows up as an obviously wrong status rather than as a plausible zero value.
package financetest

import (
	"context"

	financeapi "shopnexus/internal/module/finance/api"
	"shopnexus/internal/shared/errx"
)

// Stub implements financeapi.Service by refusing everything.
type Stub struct{}

var _ financeapi.Service = Stub{}

func (Stub) ListSessions(context.Context, financeapi.ListSessionsRequest) (financeapi.SessionPage, error) {
	return financeapi.SessionPage{}, errx.ErrNotImplemented
}

func (Stub) GetSession(context.Context, financeapi.GetSessionRequest) (financeapi.Session, error) {
	return financeapi.Session{}, errx.ErrNotImplemented
}

func (Stub) ListSessionTransactions(context.Context, financeapi.GetSessionRequest) ([]financeapi.Transaction, error) {
	return nil, errx.ErrNotImplemented
}

func (Stub) StartPayment(context.Context, financeapi.StartPaymentRequest) (financeapi.Transaction, error) {
	return financeapi.Transaction{}, errx.ErrNotImplemented
}

func (Stub) CancelSession(context.Context, financeapi.GetSessionRequest) (financeapi.Session, error) {
	return financeapi.Session{}, errx.ErrNotImplemented
}

func (Stub) ListWallets(context.Context, financeapi.ListWalletsRequest) ([]financeapi.Wallet, error) {
	return nil, errx.ErrNotImplemented
}

func (Stub) GetWallet(context.Context, financeapi.GetWalletRequest) (financeapi.Wallet, error) {
	return financeapi.Wallet{}, errx.ErrNotImplemented
}

func (Stub) ListWalletMovements(context.Context, financeapi.ListMovementsRequest) (financeapi.WalletMovementPage, error) {
	return financeapi.WalletMovementPage{}, errx.ErrNotImplemented
}

func (Stub) AdminAdjustWallet(context.Context, financeapi.AdjustWalletRequest) (financeapi.Wallet, error) {
	return financeapi.Wallet{}, errx.ErrNotImplemented
}

func (Stub) ListBankAccounts(context.Context, financeapi.ListBankAccountsRequest) ([]financeapi.BankAccount, error) {
	return nil, errx.ErrNotImplemented
}

func (Stub) CreateBankAccount(context.Context, financeapi.CreateBankAccountRequest) (financeapi.BankAccount, error) {
	return financeapi.BankAccount{}, errx.ErrNotImplemented
}

func (Stub) UpdateBankAccount(context.Context, financeapi.UpdateBankAccountRequest) (financeapi.BankAccount, error) {
	return financeapi.BankAccount{}, errx.ErrNotImplemented
}

func (Stub) DeleteBankAccount(context.Context, financeapi.DeleteBankAccountRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) CreateWithdrawal(context.Context, financeapi.CreateWithdrawalRequest) (financeapi.Withdrawal, error) {
	return financeapi.Withdrawal{}, errx.ErrNotImplemented
}

func (Stub) ListWithdrawals(context.Context, financeapi.ListWithdrawalsRequest) (financeapi.WithdrawalPage, error) {
	return financeapi.WithdrawalPage{}, errx.ErrNotImplemented
}

func (Stub) GetWithdrawal(context.Context, financeapi.WithdrawalRequest) (financeapi.Withdrawal, error) {
	return financeapi.Withdrawal{}, errx.ErrNotImplemented
}

func (Stub) CancelWithdrawal(context.Context, financeapi.WithdrawalRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) AdminApproveWithdrawal(context.Context, financeapi.ResolveWithdrawalRequest) (financeapi.Withdrawal, error) {
	return financeapi.Withdrawal{}, errx.ErrNotImplemented
}

func (Stub) AdminRejectWithdrawal(context.Context, financeapi.ResolveWithdrawalRequest) (financeapi.Withdrawal, error) {
	return financeapi.Withdrawal{}, errx.ErrNotImplemented
}

func (Stub) GetTaxInfo(context.Context, financeapi.GetTaxInfoRequest) (financeapi.TaxInfo, error) {
	return financeapi.TaxInfo{}, errx.ErrNotImplemented
}

func (Stub) PutTaxInfo(context.Context, financeapi.PutTaxInfoRequest) (financeapi.TaxInfo, error) {
	return financeapi.TaxInfo{}, errx.ErrNotImplemented
}

func (Stub) AdminVerifyTaxInfo(context.Context, financeapi.VerifyTaxInfoRequest) (financeapi.TaxInfo, error) {
	return financeapi.TaxInfo{}, errx.ErrNotImplemented
}

func (Stub) OpenCheckout(context.Context, financeapi.OpenCheckoutRequest) (financeapi.Session, error) {
	return financeapi.Session{}, errx.ErrNotImplemented
}

func (Stub) HoldEscrow(context.Context, financeapi.EscrowRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) ReleaseEscrow(context.Context, financeapi.EscrowRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) RefundEscrow(context.Context, financeapi.EscrowRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) AdminListWallets(context.Context, financeapi.AdminListWalletsRequest) ([]financeapi.Wallet, error) {
	return nil, errx.ErrNotImplemented
}
