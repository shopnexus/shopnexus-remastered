package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"shopnexus/internal/gateway/gwctx"
	"shopnexus/internal/gateway/middleware"
	"shopnexus/internal/infra/cache"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/id/idtest"
	"shopnexus/internal/shared/session"
	"shopnexus/internal/shared/token"

	"log/slog"
)

func TestMain(m *testing.M) { idtest.Install(); m.Run() }

const secret = "0123456789012345678901234567890123"

func newDeps() (*token.Manager, *session.Store) {
	return token.NewManager(secret, time.Hour), session.New(cache.NewInMemoryClient(), time.Hour)
}

// issue opens a real session and mints the token that names it — the pair the middleware
// checks.
func issue(t *testing.T, tm *token.Manager, sessions *session.Store, accountID int64) string {
	t.Helper()
	sess, err := sessions.Create(context.Background(), accountID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	tok, err := tm.Issue(token.Claims{AccountID: id.Of[id.Account](accountID).String(), SessionID: sess.ID})
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return tok
}

func serve(h http.Handler, tok string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func ok200(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }

func TestAuth_ValidTokenInjectsUserAndSession(t *testing.T) {
	tm, sessions := newDeps()
	want := id.Of[id.Account](42)
	tok := issue(t, tm, sessions, want.Int64())

	var (
		gotUID id.ID[id.Account]
		gotSID string
	)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUID, _ = gwctx.UserID(r.Context())
		gotSID = gwctx.SessionID(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	rec := serve(middleware.Auth(tm, sessions, slog.Default())(next), tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotUID != want {
		t.Fatalf("userID = %d, want %d", gotUID, want)
	}
	// Two endpoints act on the session itself, so it has to reach the handler.
	if gotSID == "" {
		t.Fatal("session id did not reach the handler")
	}
}

// A signature that is still valid is not enough: revoking a session has to take effect on
// the next request, not when the token happens to expire.
func TestAuth_RevokedSessionRejected(t *testing.T) {
	tm, sessions := newDeps()
	tok := issue(t, tm, sessions, 42)
	claims, _ := tm.Parse(tok)
	if err := sessions.Revoke(context.Background(), claims.SessionID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	rec := serve(middleware.Auth(tm, sessions, slog.Default())(http.HandlerFunc(ok200)), tok)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a revoked session", rec.Code)
	}
}

// A token whose subject and session name different accounts is forged or mixed up, not
// merely stale.
func TestAuth_SubjectSessionMismatchRejected(t *testing.T) {
	tm, sessions := newDeps()
	sess, _ := sessions.Create(context.Background(), 42)
	tok, _ := tm.Issue(token.Claims{AccountID: id.Of[id.Account](99).String(), SessionID: sess.ID})

	rec := serve(middleware.Auth(tm, sessions, slog.Default())(http.HandlerFunc(ok200)), tok)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a mismatched token", rec.Code)
	}
}

// A token whose subject is not an opaque account id must be rejected, not passed through
// as a zero id.
func TestAuth_NonOpaqueSubjectRejected(t *testing.T) {
	tm, sessions := newDeps()
	sess, _ := sessions.Create(context.Background(), 42)
	tok, _ := tm.Issue(token.Claims{AccountID: "42", SessionID: sess.ID})

	rec := serve(middleware.Auth(tm, sessions, slog.Default())(http.HandlerFunc(ok200)), tok)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestAuth_MissingHeader401(t *testing.T) {
	tm, sessions := newDeps()
	rec := serve(middleware.Auth(tm, sessions, slog.Default())(http.HandlerFunc(ok200)), "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// A well-formed token signed with a different secret must be rejected (exercises the
// Parse-failure branch, which the missing-header case short-circuits before).
func TestAuth_InvalidTokenRejected(t *testing.T) {
	signer := token.NewManager("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", time.Hour)
	verifier, sessions := newDeps()
	tok, _ := signer.Issue(token.Claims{AccountID: id.Of[id.Account](1).String(), SessionID: "sess-1"})

	rec := serve(middleware.Auth(verifier, sessions, slog.Default())(http.HandlerFunc(ok200)), tok)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// OptionalAuth lets an anonymous caller through, and still refuses a bad token rather than
// quietly degrading it to anonymous.
func TestOptionalAuth_AnonymousPassesAndBadTokenFails(t *testing.T) {
	tm, sessions := newDeps()
	h := middleware.OptionalAuth(tm, sessions, slog.Default())(http.HandlerFunc(ok200))

	if rec := serve(h, ""); rec.Code != http.StatusOK {
		t.Fatalf("anonymous status = %d, want 200", rec.Code)
	}
	if rec := serve(h, "garbage"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad token status = %d, want 401", rec.Code)
	}
}
