package authmodel

import sharedmodel "shopnexus-remastered/internal/module/shared/model"

var (
	ErrInvalidCredentials = sharedmodel.NewError("auth.invalid_credentials", "Invalid credentials provided")
	ErrAccountNotFound    = sharedmodel.NewError("auth.account_not_found", "Account not found")
	ErrMissingIdentifier  = sharedmodel.NewError("auth.missing_identifier", "At least one of username, email, or phone must be provided")
)
