// Package handler holds the thin HTTP handlers. A handler decodes and validates
// the request, calls the module's api.Service, and writes the result; it holds no
// business logic of its own.
package handler

import (
	"log/slog"
	"net/http"

	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/httpx"
)

// notImplemented answers a route the OpenAPI contract declares but nothing
// implements yet.
//
// 501 and not 404, because the two say different things: 404 means the caller got
// the URL wrong, 501 means the URL is right and the feature is missing. Wiring a
// documented route to a 404 would make a client debug its own request forever.
func notImplemented(w http.ResponseWriter, log *slog.Logger) {
	httpx.WriteError(w, log, errx.ErrNotImplemented)
}
