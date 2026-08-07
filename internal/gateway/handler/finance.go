package handler

import (
	"log/slog"
	"net/http"

	"github.com/go-playground/validator/v10"

	financeapi "shopnexus/internal/module/finance/api"
	"shopnexus/internal/shared/httpx"
	"shopnexus/internal/shared/id"
)

// Finance serves the finance module's routes: payment sessions and their rail legs,
// wallets and their ledger, bank accounts, withdrawals and tax registration.
//
// Thin, like every handler here: read the request, fill in what only the gateway knows,
// call the service, write the result. Whether the caller may see a balance or resolve a
// withdrawal is the service's decision, because the role is a row in the account
// module's table.
type Finance struct {
	svc financeapi.Service
	v   *validator.Validate
	log *slog.Logger
}

func NewFinance(svc financeapi.Service, v *validator.Validate, log *slog.Logger) *Finance {
	return &Finance{svc: svc, v: v, log: log}
}

// --- payment sessions ---

// ListPaymentSessions handles GET /payment-sessions.
func (h *Finance) ListPaymentSessions(w http.ResponseWriter, r *http.Request) {
	h.listSessions(w, r, false)
}

// AdminListPaymentSessions handles GET /admin/payment-sessions — every account's.
func (h *Finance) AdminListPaymentSessions(w http.ResponseWriter, r *http.Request) {
	h.listSessions(w, r, true)
}

func (h *Finance) listSessions(w http.ResponseWriter, r *http.Request, admin bool) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	page, limit, err := pageParams(r)
	if failed(w, h.log, err) {
		return
	}
	// The three staff filters the spec has always advertised. Reading them here is what
	// makes that true: until now a date range was accepted and silently ignored, which on
	// a reconciliation screen answers a question nobody asked.
	from, err := optionalTimeParam(r, "from")
	if failed(w, h.log, err) {
		return
	}
	to, err := optionalTimeParam(r, "to")
	if failed(w, h.log, err) {
		return
	}
	accountID, err := optionalIDParam[id.Account](r, "account_id")
	if failed(w, h.log, err) {
		return
	}
	req := financeapi.ListSessionsRequest{
		ActorID:   uid,
		Admin:     admin,
		Role:      r.URL.Query().Get("role"),
		Kind:      r.URL.Query().Get("kind"),
		Status:    r.URL.Query().Get("status"),
		AccountID: accountID,
		From:      from,
		To:        to,
		Page:      page,
		Limit:     limit,
	}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.ListSessions(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WritePage(w, http.StatusOK, res.Data, httpx.PageMeta(res.Meta))
}

// GetPaymentSession handles GET /payment-sessions/{id}.
func (h *Finance) GetPaymentSession(w http.ResponseWriter, r *http.Request) {
	req, err := h.sessionRequest(r)
	if failed(w, h.log, err) {
		return
	}
	res, err := h.svc.GetSession(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// ListTransactions handles GET /payment-sessions/{id}/transactions.
func (h *Finance) ListTransactions(w http.ResponseWriter, r *http.Request) {
	req, err := h.sessionRequest(r)
	if failed(w, h.log, err) {
		return
	}
	res, err := h.svc.ListSessionTransactions(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// StartPayment handles POST /payment-sessions/{id}/payments. 201 for the leg, which is
// pending: the provider's webhook settles it, so this is not a receipt.
func (h *Finance) StartPayment(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	sessionID, err := pathID[id.PaymentSession](r, "id")
	if failed(w, h.log, err) {
		return
	}
	var req financeapi.StartPaymentRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID, req.ID = uid, sessionID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.StartPayment(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusCreated, res)
}

// CancelPaymentSession handles POST /payment-sessions/{id}/cancellation.
func (h *Finance) CancelPaymentSession(w http.ResponseWriter, r *http.Request) {
	req, err := h.sessionRequest(r)
	if failed(w, h.log, err) {
		return
	}
	res, err := h.svc.CancelSession(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

func (h *Finance) sessionRequest(r *http.Request) (financeapi.GetSessionRequest, error) {
	uid, err := actor(r)
	if err != nil {
		return financeapi.GetSessionRequest{}, err
	}
	sessionID, err := pathID[id.PaymentSession](r, "id")
	if err != nil {
		return financeapi.GetSessionRequest{}, err
	}
	req := financeapi.GetSessionRequest{ActorID: uid, ID: sessionID}
	return req, check(h.v, req)
}

// --- wallets ---

// ListWallets handles GET /wallets — every currency the caller holds.
func (h *Finance) ListWallets(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	req := financeapi.ListWalletsRequest{ActorID: uid}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.ListWallets(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// GetWallet handles GET /wallets/{currency}.
func (h *Finance) GetWallet(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	req := financeapi.GetWalletRequest{ActorID: uid, AccountID: uid, Currency: r.PathValue("currency")}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.GetWallet(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// AdminGetWallets handles GET /admin/wallets/{accountID}. The currency is a query
// parameter here: an admin looking at somebody's money starts from the account.
func (h *Finance) AdminGetWallets(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	accountID, err := pathID[id.Account](r, "accountID")
	if failed(w, h.log, err) {
		return
	}
	// Every currency the account holds: a support agent looking at a balance dispute does
	// not know which one it is in, so this takes no currency at all.
	req := financeapi.AdminListWalletsRequest{ActorID: uid, AccountID: accountID}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.AdminListWallets(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// ListWalletTransactions handles GET /wallets/{currency}/transactions — the statement.
func (h *Finance) ListWalletTransactions(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	page, limit, err := pageParams(r)
	if failed(w, h.log, err) {
		return
	}
	req := financeapi.ListMovementsRequest{
		ActorID:  uid,
		Currency: r.PathValue("currency"),
		Kind:     r.URL.Query().Get("kind"),
		Page:     page,
		Limit:    limit,
	}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.ListWalletMovements(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WritePage(w, http.StatusOK, res.Data, httpx.PageMeta(res.Meta))
}

// AdminAdjustWallet handles POST /admin/wallets/{accountID}/adjustments.
func (h *Finance) AdminAdjustWallet(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	accountID, err := pathID[id.Account](r, "accountID")
	if failed(w, h.log, err) {
		return
	}
	var req financeapi.AdjustWalletRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID, req.AccountID = uid, accountID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.AdminAdjustWallet(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// --- bank accounts ---

// ListBankAccounts handles GET /bank-accounts.
func (h *Finance) ListBankAccounts(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	req := financeapi.ListBankAccountsRequest{ActorID: uid}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.ListBankAccounts(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// CreateBankAccount handles POST /bank-accounts.
func (h *Finance) CreateBankAccount(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	var req financeapi.CreateBankAccountRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID = uid
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.CreateBankAccount(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusCreated, res)
}

// UpdateBankAccount handles PATCH /bank-accounts/{id} — only the default flag moves. A
// changed number is a different destination, so that is a new registration.
func (h *Finance) UpdateBankAccount(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	bankAccountID, err := pathID[id.BankAccount](r, "id")
	if failed(w, h.log, err) {
		return
	}
	var req financeapi.UpdateBankAccountRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID, req.ID = uid, bankAccountID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.UpdateBankAccount(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// DeleteBankAccount handles DELETE /bank-accounts/{id} — soft, so a past withdrawal
// keeps its payee.
func (h *Finance) DeleteBankAccount(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	bankAccountID, err := pathID[id.BankAccount](r, "id")
	if failed(w, h.log, err) {
		return
	}
	req := financeapi.DeleteBankAccountRequest{ActorID: uid, ID: bankAccountID}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	if failed(w, h.log, h.svc.DeleteBankAccount(r.Context(), req)) {
		return
	}
	httpx.WriteNoContent(w)
}

// --- withdrawals ---

// CreateWithdrawal handles POST /withdrawals. The balance is debited here, before an
// admin sees it: the same money must not be withdrawable twice while the queue is worked.
func (h *Finance) CreateWithdrawal(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	var req financeapi.CreateWithdrawalRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID = uid
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.CreateWithdrawal(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusCreated, res)
}

// ListWithdrawals handles GET /withdrawals.
func (h *Finance) ListWithdrawals(w http.ResponseWriter, r *http.Request) {
	h.listWithdrawals(w, r, false)
}

// AdminListWithdrawals handles GET /admin/withdrawals — the queue a human works.
func (h *Finance) AdminListWithdrawals(w http.ResponseWriter, r *http.Request) {
	h.listWithdrawals(w, r, true)
}

func (h *Finance) listWithdrawals(w http.ResponseWriter, r *http.Request, admin bool) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	page, limit, err := pageParams(r)
	if failed(w, h.log, err) {
		return
	}
	req := financeapi.ListWithdrawalsRequest{
		ActorID: uid,
		Admin:   admin,
		Status:  r.URL.Query().Get("status"),
		Page:    page,
		Limit:   limit,
	}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.ListWithdrawals(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WritePage(w, http.StatusOK, res.Data, httpx.PageMeta(res.Meta))
}

// GetWithdrawal handles GET /withdrawals/{id}.
func (h *Finance) GetWithdrawal(w http.ResponseWriter, r *http.Request) {
	req, err := h.withdrawalRequest(r)
	if failed(w, h.log, err) {
		return
	}
	res, err := h.svc.GetWithdrawal(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// CancelWithdrawal handles DELETE /withdrawals/{id} — the requester changing their
// mind, which returns the debited money in the same call.
func (h *Finance) CancelWithdrawal(w http.ResponseWriter, r *http.Request) {
	req, err := h.withdrawalRequest(r)
	if failed(w, h.log, err) {
		return
	}
	if failed(w, h.log, h.svc.CancelWithdrawal(r.Context(), req)) {
		return
	}
	httpx.WriteNoContent(w)
}

func (h *Finance) withdrawalRequest(r *http.Request) (financeapi.WithdrawalRequest, error) {
	uid, err := actor(r)
	if err != nil {
		return financeapi.WithdrawalRequest{}, err
	}
	withdrawalID, err := pathID[id.PaymentSession](r, "id")
	if err != nil {
		return financeapi.WithdrawalRequest{}, err
	}
	req := financeapi.WithdrawalRequest{ActorID: uid, ID: withdrawalID}
	return req, check(h.v, req)
}

// AdminApproveWithdrawal handles POST /admin/withdrawals/{id}/approval.
func (h *Finance) AdminApproveWithdrawal(w http.ResponseWriter, r *http.Request) {
	req, err := h.resolveRequest(r, true)
	if failed(w, h.log, err) {
		return
	}
	res, err := h.svc.AdminApproveWithdrawal(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// AdminRejectWithdrawal handles POST /admin/withdrawals/{id}/rejection. The reason is
// required, and the service is what enforces it: somebody's cash-out did not happen and
// they are owed the why.
func (h *Finance) AdminRejectWithdrawal(w http.ResponseWriter, r *http.Request) {
	req, err := h.resolveRequest(r, false)
	if failed(w, h.log, err) {
		return
	}
	res, err := h.svc.AdminRejectWithdrawal(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// resolveRequest reads an admin's decision. An approval may carry no body at all —
// there is nothing it has to say.
func (h *Finance) resolveRequest(r *http.Request, optionalBody bool) (financeapi.ResolveWithdrawalRequest, error) {
	uid, err := actor(r)
	if err != nil {
		return financeapi.ResolveWithdrawalRequest{}, err
	}
	withdrawalID, err := pathID[id.PaymentSession](r, "id")
	if err != nil {
		return financeapi.ResolveWithdrawalRequest{}, err
	}
	var req financeapi.ResolveWithdrawalRequest
	decode := decodeBody
	if optionalBody {
		decode = decodeOptionalBody
	}
	if err := decode(r, &req); err != nil {
		return financeapi.ResolveWithdrawalRequest{}, err
	}
	req.ActorID, req.ID = uid, withdrawalID
	return req, check(h.v, req)
}

// --- tax registration ---

// GetTaxInfo handles GET /tax-info.
func (h *Finance) GetTaxInfo(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	req := financeapi.GetTaxInfoRequest{ActorID: uid}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.GetTaxInfo(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// UpsertTaxInfo handles PUT /tax-info. Filing again resets the verdict: the details
// changed, so the previous verification was of something else.
func (h *Finance) UpsertTaxInfo(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	var req financeapi.PutTaxInfoRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID = uid
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.PutTaxInfo(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// AdminVerifyTaxInfo handles POST /admin/tax-info/{accountID}/verification.
func (h *Finance) AdminVerifyTaxInfo(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	accountID, err := pathID[id.Account](r, "accountID")
	if failed(w, h.log, err) {
		return
	}
	var req financeapi.VerifyTaxInfoRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID, req.AccountID = uid, accountID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.AdminVerifyTaxInfo(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}
