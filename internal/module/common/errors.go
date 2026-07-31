package common

import (
	"net/http"

	"shopnexus/internal/shared/errx"
)

// The errors the shared tables produce. They live here for the same reason a module's live in
// its own domain: the adapter has to return them, and the import stays one-way.
var (
	ErrResourceNotFound = errx.NewError(http.StatusNotFound, "resource_not_found", "resource not found")
	ErrOptionNotFound   = errx.NewError(http.StatusNotFound, "option_not_found", "option not found")
	ErrDuplicateObject  = errx.NewError(http.StatusConflict, "resource_object_key_taken", "provider already stores this object key")
)
