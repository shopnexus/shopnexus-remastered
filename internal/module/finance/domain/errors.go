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
)
