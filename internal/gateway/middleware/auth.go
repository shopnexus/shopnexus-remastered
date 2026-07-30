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
	"shopnexus/internal/shared/session"
	"shopnexus/internal/shared/token"
)

// Auth establishes the caller from the access token *and* the session it names.
//
// The signature check alone is not enough: a JWT stays valid until it expires, while a
// logout, a password change or a suspension has to take effect now. So every
// authenticated request costs one session lookup in Redis, which is the price of
// revocation being real rather than eventual.
func Auth(tokens *token.Manager, sessions *session.Store, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			raw, ok := strings.CutPrefix(header, "Bearer ")
			if !ok || raw == "" {
				httpx.WriteError(w, log, errx.ErrUnauthorized)
				return
			}
			claims, err := tokens.Parse(raw)
			if err != nil {
				httpx.WriteError(w, log, errx.ErrInvalidToken)
				return
			}
			// The subject is the opaque account id, so the raw database key never travels
			// inside a token the client can decode and read.
			uid, err := id.Parse[id.Account](claims.AccountID)
			if err != nil {
				httpx.WriteError(w, log, errx.ErrInvalidToken)
				return
			}
			accountID, err := sessions.Lookup(r.Context(), claims.SessionID)
			if err != nil {
				httpx.WriteError(w, log, err)
				return
			}
			// A token whose subject and session disagree is not a stale credential, it is a
			// forged or mixed-up one. Refuse it rather than trusting either half.
			if accountID != uid.Int64() {
				log.Warn("token subject does not match its session",
					"session_account_id", accountID, "token_account_id", uid.Int64())
				httpx.WriteError(w, log, errx.ErrInvalidToken)
				return
			}
			ctx := gwctx.WithUserID(r.Context(), uid)
			ctx = gwctx.WithSessionID(ctx, claims.SessionID)
			next.ServeHTTP(w, r.WithContext(ctx))
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
func OptionalAuth(tokens *token.Manager, sessions *session.Store, log *slog.Logger) func(http.Handler) http.Handler {
	authed := Auth(tokens, sessions, log)
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
