// Package middleware: HTTP middleware for the gateway.
package middleware

import (
	"log/slog"
	"net/http"
	"strings"

	"shopnexus/internal/gateway/gwctx"
	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/httpx"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/token"
)

func Auth(tokens *token.Manager, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			raw, ok := strings.CutPrefix(header, "Bearer ")
			if !ok || raw == "" {
				httpx.WriteError(w, log, errx.ErrUnauthorized)
				return
			}
			sub, err := tokens.Parse(raw)
			if err != nil {
				httpx.WriteError(w, log, errx.ErrInvalidToken)
				return
			}
			// The subject is the opaque account id, so the raw database key never
			// travels inside a token the client can decode and read.
			uid, err := id.Parse[id.Account](sub)
			if err != nil {
				httpx.WriteError(w, log, errx.ErrInvalidToken)
				return
			}
			next.ServeHTTP(w, r.WithContext(gwctx.WithUserID(r.Context(), uid)))
		})
	}
}
