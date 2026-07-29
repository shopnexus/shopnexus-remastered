package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"shopnexus/internal/gateway/gwctx"
	"shopnexus/internal/gateway/middleware"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/id/idtest"
	"shopnexus/internal/shared/token"

	"log/slog"
)

func TestMain(m *testing.M) { idtest.Install(); m.Run() }

func TestAuth_ValidTokenInjectsUserID(t *testing.T) {
	tm := token.NewManager("0123456789012345678901234567890123", time.Hour)
	want := id.Of[id.Account](42)
	tok, _ := tm.Issue(want.String())

	var gotUID id.ID[id.Account]
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUID, _ = gwctx.UserID(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	h := middleware.Auth(tm, slog.Default())(next)

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotUID != want {
		t.Fatalf("userID = %d, want %d", gotUID, want)
	}
}

// A token whose subject is not an opaque account id must be rejected, not passed
// through as a zero id.
func TestAuth_NonOpaqueSubjectRejected(t *testing.T) {
	tm := token.NewManager("0123456789012345678901234567890123", time.Hour)
	tok, _ := tm.Issue("42")

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := middleware.Auth(tm, slog.Default())(next)

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestAuth_MissingHeader401(t *testing.T) {
	tm := token.NewManager("0123456789012345678901234567890123", time.Hour)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	h := middleware.Auth(tm, slog.Default())(next)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// A well-formed token signed with a different secret must be rejected (exercises
// the Parse-failure branch, which the missing-header case short-circuits before).
func TestAuth_InvalidTokenRejected(t *testing.T) {
	signer := token.NewManager("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", time.Hour)
	verifier := token.NewManager("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", time.Hour)
	tok, _ := signer.Issue("user-1")

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := middleware.Auth(verifier, slog.Default())(next)

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
