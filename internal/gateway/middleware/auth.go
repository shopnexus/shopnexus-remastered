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

// OptionalAuth establishes the caller when a token is present and lets the request
// through anonymously when it is not.
//
// It serves the reads whose result widens for a known caller — a listing feed that can
// be personalised, a draft only its owner may see — where the same route still has to
// answer an anonymous visitor. A token that is present but bad is still rejected:
// degrading it to anonymous would hide an expiry behind a page of generic results.
func OptionalAuth(tokens *token.Manager, log *slog.Logger) func(http.Handler) http.Handler {
	authed := Auth(tokens, log)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") == "" {
				next.ServeHTTP(w, r)
				return
			}
			authed(next).ServeHTTP(w, r)
		})
	}
}
