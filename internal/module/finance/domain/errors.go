package domain

import (
	"net/http"

	"shopnexus/internal/shared/errx"
)

// Payment errors. Not-found lives here so the postgres adapter can produce it
// without importing the module root package.
var (
	ErrSessionNotFound      = errx.NewError(http.StatusNotFound, "payment_session_not_found", "payment session not found")
	ErrWalletNotFound       = errx.NewError(http.StatusNotFound, "wallet_not_found", "wallet not found")
	ErrSessionExpiryInvalid = errx.NewError(http.StatusBadRequest, "session_expiry_invalid", "session expiry must be in the future")
	ErrInsufficientBalance  = errx.NewError(http.StatusConflict, "insufficient_balance", "wallet balance is too low")

	// --- the wallet ledger ---
	// ErrEmptyMovement is a ledger movement with both deltas zero. 400: an admin
	// adjustment route accepts the numbers, so a caller can ask for one.
	// accepts a zero movement, so reaching this means a service built one.
	ErrEmptyMovement         = errx.NewError(http.StatusBadRequest, "empty_movement", "a ledger movement has to move something")
	ErrMovementAlreadyPosted = errx.NewError(http.StatusConflict, "movement_already_posted", "this movement was already posted")

	// --- sessions and their legs ---
	ErrSessionSettled    = errx.NewError(http.StatusConflict, "payment_session_settled", "this payment session is already settled")
	ErrSessionExpired    = errx.NewError(http.StatusConflict, "payment_session_expired", "this payment session has expired")
	ErrSessionNotPayable = errx.NewError(http.StatusConflict, "payment_session_not_payable", "only a pending session can be paid")
	// ErrSessionKindNotPayable and ErrSessionKindNotCancellable keep the payment-session
	// routes to the one kind a payer actually tenders. A withdrawal shares the id space and
	// names its requester as the payer, so without these it could be driven to `success`
	// through the checkout route, or cancelled without the debit being returned.
	ErrSessionKindNotPayable     = errx.NewError(http.StatusConflict, "payment_session_kind_not_payable", "this session is not tendered on a payment rail")
	ErrSessionKindNotCancellable = errx.NewError(http.StatusConflict, "payment_session_kind_not_cancellable", "cancel a withdrawal through the withdrawal route, so the money is returned")
	// ErrReturnURLNotAllowed is a redirect target that is not the platform's. Unchecked it
	// would be an open redirect wearing a payment flow.
	ErrReturnURLNotAllowed = errx.NewError(http.StatusBadRequest, "return_url_not_allowed", "that return URL is not one this platform redirects to")
	// ErrLegAlreadyBooked is a rail leg the ledger already has — a provider reference reused,
	// or a reversal of one already reversed. Its own error rather than the wallet ledger's: a
	// rail leg and a wallet movement are different rows with different keys, and a code that
	// names the wrong one sends a reader to the wrong table.
	ErrLegAlreadyBooked         = errx.NewError(http.StatusConflict, "transaction_already_booked", "this rail leg is already recorded")
	ErrTransactionNotFound      = errx.NewError(http.StatusNotFound, "transaction_not_found", "transaction not found")
	ErrTransactionSettled       = errx.NewError(http.StatusConflict, "transaction_settled", "this leg is already settled")
	ErrTransactionStatusInvalid = errx.NewError(http.StatusUnprocessableEntity, "transaction_status_invalid", "a leg settles as success or failed")
	ErrChargeAmountInvalid      = errx.NewError(http.StatusUnprocessableEntity, "charge_amount_invalid", "a charge is positive and a reversal cannot exceed it")
	ErrReversalNeedsSuccess     = errx.NewError(http.StatusConflict, "reversal_needs_success", "only a settled charge can be reversed")
	ErrPaymentOptionUnknown     = errx.NewError(http.StatusUnprocessableEntity, "payment_option_unknown", "no enabled payment option by that id")

	// --- bank accounts ---
	ErrBankAccountNotFound  = errx.NewError(http.StatusNotFound, "bank_account_not_found", "bank account not found")
	ErrBankCodeInvalid      = errx.NewError(http.StatusUnprocessableEntity, "bank_code_invalid", "bank code must be a lowercase slug such as vcb")
	ErrAccountNumberInvalid = errx.NewError(http.StatusUnprocessableEntity, "account_number_invalid", "account number must be digits")
	ErrBankAccountInUse     = errx.NewError(http.StatusConflict, "bank_account_in_use", "a withdrawal to this account is still in flight")

	// --- withdrawals ---
	ErrWithdrawalNotFound = errx.NewError(http.StatusNotFound, "withdrawal_not_found", "withdrawal not found")
	ErrWithdrawalSettled  = errx.NewError(http.StatusConflict, "withdrawal_settled", "this withdrawal has already been decided")
	// ErrPayeeUnverified gates real money leaving the platform on the same identity flag
	// that gates selling: a payout to somebody unidentified is the one mistake that
	// cannot be undone.
	ErrPayeeUnverified      = errx.NewError(http.StatusUnprocessableEntity, "payee_unverified", "identity verification is required before withdrawing")
	ErrRejectionNeedsReason = errx.NewError(http.StatusUnprocessableEntity, "rejection_needs_reason", "a rejected withdrawal needs a reason")

	// --- tax info ---
	ErrTaxInfoNotFound = errx.NewError(http.StatusNotFound, "tax_info_not_found", "no tax registration on file")
	ErrTaxCodeInvalid  = errx.NewError(http.StatusUnprocessableEntity, "tax_code_invalid", "a Vietnamese tax code is ten digits, optionally with a three-digit branch")
	ErrTaxInfoSettled  = errx.NewError(http.StatusConflict, "tax_info_settled", "this registration was already decided; file again to reset it")
	ErrTaxCodeTaken    = errx.NewError(http.StatusConflict, "tax_code_taken", "another account has verified this tax code")

	// --- authorization ---
	ErrAdminRequired = errx.NewError(http.StatusForbidden, "admin_required", "admin role required")
)
