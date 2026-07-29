package domain

import (
	"net/http"

	"shopnexus/internal/shared/errx"
)

// Common module errors. Not-found lives here so the postgres adapter can produce
// it without importing the module root package.
var (
	ErrResourceNotFound = errx.NewError(http.StatusNotFound, "resource_not_found", "resource not found")
	ErrOptionNotFound   = errx.NewError(http.StatusNotFound, "option_not_found", "option not found")
	ErrDuplicateObject  = errx.NewError(http.StatusConflict, "resource_object_key_taken", "provider already stores this object key")
)
