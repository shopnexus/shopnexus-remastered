package domain

import (
	"net/http"

	"shopnexus/internal/shared/errx"
)

// Account errors. Not-found lives here so the postgres adapter can produce it
// without importing the module root package; the rest are business rules
// enforced by the service.
var (
	ErrAccountNotFound    = errx.NewError(http.StatusNotFound, "account_not_found", "account not found")
	ErrEmailTaken         = errx.NewError(http.StatusConflict, "email_taken", "email already registered")
	ErrInvalidCredentials = errx.NewError(http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
)
