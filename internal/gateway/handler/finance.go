package handler

import (
	"log/slog"
	"net/http"

	"github.com/go-playground/validator/v10"

	financeapi "shopnexus/internal/module/finance/api"
)

// Finance serves the finance module's routes: payment sessions, wallets, withdrawals, bank accounts and tax registration.
//
// Scaffold. Every method answers 501 until it is written, and the routes are
// registered in router.go so the OpenAPI contract test can hold the two in step.
// The service, validator and logger are held already: it keeps the fx graph real —
// so the module's pool is opened and its config validated at startup — and makes
// filling a method in a local edit rather than a rewiring.
type Finance struct {
	svc financeapi.Service
	v   *validator.Validate
	log *slog.Logger
}

func NewFinance(svc financeapi.Service, v *validator.Validate, log *slog.Logger) *Finance {
	return &Finance{svc: svc, v: v, log: log}
}

// ListPaymentSessions handles GET /payment-sessions.
func (h *Finance) ListPaymentSessions(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// GetPaymentSession handles GET /payment-sessions/{id}.
func (h *Finance) GetPaymentSession(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// ListTransactions handles GET /payment-sessions/{id}/transactions.
func (h *Finance) ListTransactions(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// StartPayment handles POST /payment-sessions/{id}/payments.
func (h *Finance) StartPayment(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// CancelPaymentSession handles POST /payment-sessions/{id}/cancellation.
func (h *Finance) CancelPaymentSession(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// ListWallets handles GET /wallets.
func (h *Finance) ListWallets(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// GetWallet handles GET /wallets/{currency}.
func (h *Finance) GetWallet(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// ListWalletTransactions handles GET /wallets/{currency}/transactions.
func (h *Finance) ListWalletTransactions(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// CreateWithdrawal handles POST /withdrawals.
func (h *Finance) CreateWithdrawal(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// ListWithdrawals handles GET /withdrawals.
func (h *Finance) ListWithdrawals(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// GetWithdrawal handles GET /withdrawals/{id}.
func (h *Finance) GetWithdrawal(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// CancelWithdrawal handles DELETE /withdrawals/{id}.
func (h *Finance) CancelWithdrawal(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// ListBankAccounts handles GET /bank-accounts.
func (h *Finance) ListBankAccounts(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// CreateBankAccount handles POST /bank-accounts.
func (h *Finance) CreateBankAccount(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// UpdateBankAccount handles PATCH /bank-accounts/{id}.
func (h *Finance) UpdateBankAccount(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// DeleteBankAccount handles DELETE /bank-accounts/{id}.
func (h *Finance) DeleteBankAccount(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// GetTaxInfo handles GET /tax-info.
func (h *Finance) GetTaxInfo(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// UpsertTaxInfo handles PUT /tax-info.
func (h *Finance) UpsertTaxInfo(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AdminListWithdrawals handles GET /admin/withdrawals.
func (h *Finance) AdminListWithdrawals(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AdminApproveWithdrawal handles POST /admin/withdrawals/{id}/approval.
func (h *Finance) AdminApproveWithdrawal(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AdminRejectWithdrawal handles POST /admin/withdrawals/{id}/rejection.
func (h *Finance) AdminRejectWithdrawal(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AdminListPaymentSessions handles GET /admin/payment-sessions.
func (h *Finance) AdminListPaymentSessions(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AdminGetWallets handles GET /admin/wallets/{accountID}.
func (h *Finance) AdminGetWallets(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AdminAdjustWallet handles POST /admin/wallets/{accountID}/adjustments.
func (h *Finance) AdminAdjustWallet(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AdminVerifyTaxInfo handles POST /admin/tax-info/{accountID}/verification.
func (h *Finance) AdminVerifyTaxInfo(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}
