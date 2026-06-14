package accountmodel

import (
	"net/http"
	"shopnexus-server/internal/shared/errors"
)

// Sentinel errors for the account module.
var (
	ErrInvalidCredentials = errors.NewError(http.StatusUnauthorized, "invalid_credentials", "Invalid credentials provided")
	ErrAccountNotFound    = errors.NewError(http.StatusNotFound, "account_not_found", "Account not found")
	ErrMissingIdentifier  = errors.NewError(
		http.StatusBadRequest,
		"missing_identifier",
		"At least one of username, email, or phone must be provided",
	)
	ErrEmailRequiredForOAuth = errors.NewError(
		http.StatusBadRequest,
		"email_required_for_oauth",
		"Email is required when password is not provided",
	)
	ErrContactNotFound  = errors.NewError(http.StatusNotFound, "contact_not_found", "The contact could not be found")
	ErrNoDefaultContact = errors.NewError(
		http.StatusNotFound,
		"no_default_contact",
		"Some accounts do not have a default contact address",
	)
	ErrCannotDeleteLastContact  = errors.NewError(http.StatusConflict, "cannot_delete_last_contact", "Cannot delete the only contact address")
	ErrCardPaymentNotConfigured = errors.NewError(http.StatusNotImplemented, "card_payment_not_configured", "card payment not configured")

	ErrInvalidCountry                = errors.NewErrorf(http.StatusBadRequest, "invalid_country", "invalid country: %v")
	ErrContactAddressCountryMismatch = errors.NewErrorf(
		http.StatusBadRequest,
		"address_country_mismatch",
		"location resolves to %s, profile country is %s",
	)
	ErrContactCoordsPair = errors.NewError(
		http.StatusBadRequest,
		"contact_coords_pair",
		"latitude and longitude must be provided together",
	)
	ErrWalletNotEmpty = errors.NewErrorf(
		http.StatusConflict,
		"wallet_not_empty",
		"wallet balance is %d, must be zero to change country",
	)
)
