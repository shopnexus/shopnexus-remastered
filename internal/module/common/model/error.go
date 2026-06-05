package commonmodel

import (
	"net/http"
	"shopnexus-server/internal/shared/errors"
)

// Sentinel errors for the common module.
var (
	ErrResourceNotFound         = errors.NewError(http.StatusNotFound, "resource_not_found", "Resource not found")
	ErrEmptyAddress             = errors.NewError(http.StatusBadRequest, "empty_address", "address is empty")
	ErrAddressNotFound          = errors.NewError(http.StatusBadRequest, "address_not_found", "address could not be located")
	ErrAddressCountryUnresolved = errors.NewError(
		http.StatusBadRequest,
		"address_country_unresolved",
		"could not verify address country (no country in geocode result)",
	)
)
