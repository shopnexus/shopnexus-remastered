package common

import (
	"net/http"

	"shopnexus/internal/shared/errx"
)

// The errors the shared tables produce. They live here for the same reason a module's live in
// its own domain: the adapter has to return them, and the import stays one-way.
var (
	ErrResourceNotFound = errx.NewError(http.StatusNotFound, "resource_not_found", "resource not found")
	ErrDuplicateObject  = errx.NewError(http.StatusConflict, "resource_object_key_taken", "provider already stores this object key")
	ErrOptionNotFound   = errx.NewError(http.StatusNotFound, "option_not_found", "no option by that id")
	// Absent and unknown are told apart, because they are different mistakes: a client that forgot
	// the parameter gets 400, one that asked for a category nobody defined gets 404.
	ErrOptionCategoryRequired = errx.NewError(http.StatusBadRequest, "option_category_required", "a category is required")
	// A category nobody defined, and a defined one a user may not see, answer the same way on
	// purpose: telling them apart would let anyone enumerate the platform's operator surface.
	ErrOptionCategoryUnknown = errx.NewError(http.StatusNotFound, "option_category_unknown", "no such option category")
	// The provider is what a row resolves to a client, so one nobody registered is a row the
	// deployment cannot serve — 422 rather than 404: the row exists, the vendor does not.
	ErrOptionProviderUnknown = errx.NewError(http.StatusUnprocessableEntity, "option_provider_unknown", "no provider registered by that name")
)
