package domain

import (
	"net/http"

	"shopnexus/internal/shared/errx"
)

// ErrOrderNotFound is returned by the repository when an order does not exist.
// Lives in domain so the postgres adapter can produce it.
var ErrOrderNotFound = errx.NewError(http.StatusNotFound, "order_not_found", "order not found")
