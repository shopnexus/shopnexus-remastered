package authmodel

import sharedmodel "shopnexus-remastered/internal/module/shared/model"

var (
	ErrInvalidCredentials = sharedmodel.NewError("auth.invalid_credentials", "Invalid credentials provided")
)
