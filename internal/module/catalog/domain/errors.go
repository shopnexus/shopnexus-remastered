package domain

import (
	"net/http"

	"shopnexus/internal/shared/errx"
)

// ErrListingNotFound is returned by the repository when a listing does not
// exist. Lives in domain so the postgres adapter can produce it.
var ErrListingNotFound = errx.NewError(http.StatusNotFound, "listing_not_found", "listing not found")
