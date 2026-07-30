package session_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"shopnexus/internal/infra/cache"
	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/session"
)

func newStore() *session.Store {
	return session.New(cache.NewInMemoryClient(), time.Hour)
}

func TestCreateLookup_RoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newStore()

	sess, err := s.Create(ctx, 42)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sess.ID == "" || sess.RefreshToken == "" {
		t.Fatalf("session = %+v, want an id and a refresh token", sess)
	}
	got, err := s.Lookup(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got != 42 {
		t.Fatalf("account = %d, want 42", got)
	}
}

func TestLookup_UnknownSessionIsUnauthorized(t *testing.T) {
	_, err := newStore().Lookup(context.Background(), "nope")
	if status, _, _, ok := errx.Decompose(err); !ok || status != 401 {
		t.Fatalf("expected 401, got %v", err)
	}
	if !errors.Is(err, session.ErrInvalidSession) {
		t.Fatalf("expected ErrInvalidSession, got %v", err)
	}
}

// A logout has to take effect against a token already in circulation, which is the whole
// reason the session is looked up per request.
func TestRevoke_EndsTheSession(t *testing.T) {
	ctx := context.Background()
	s := newStore()
	sess, _ := s.Create(ctx, 7)

	if err := s.Revoke(ctx, sess.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := s.Lookup(ctx, sess.ID); !errors.Is(err, session.ErrInvalidSession) {
		t.Fatalf("session survived a revoke: %v", err)
	}
}

// Rotation: the refresh token that was used stops working, and the new one continues the
// same session, so the access token that follows names a session the client already has.
func TestRotate_ReplacesTheRefreshTokenAndKeepsTheSession(t *testing.T) {
	ctx := context.Background()
	s := newStore()
	first, _ := s.Create(ctx, 9)

	second, err := s.Rotate(ctx, first.RefreshToken)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("session id changed on refresh: %q -> %q", first.ID, second.ID)
	}
	if second.RefreshToken == first.RefreshToken {
		t.Error("refresh token was not rotated")
	}
	if _, err := s.Rotate(ctx, first.RefreshToken); !errors.Is(err, session.ErrInvalidSession) {
		t.Errorf("the used refresh token still works: %v", err)
	}
}

// Suspending an account, or resetting its password, has to drop every session at once —
// and without keeping a list of them.
func TestRevokeAll_EndsEverySession(t *testing.T) {
	ctx := context.Background()
	s := newStore()
	a, _ := s.Create(ctx, 1)
	b, _ := s.Create(ctx, 1)
	other, _ := s.Create(ctx, 2)

	if err := s.RevokeAll(ctx, 1, ""); err != nil {
		t.Fatalf("RevokeAll: %v", err)
	}
	for _, sess := range []string{a.ID, b.ID} {
		if _, err := s.Lookup(ctx, sess); !errors.Is(err, session.ErrInvalidSession) {
			t.Errorf("session %q survived RevokeAll: %v", sess, err)
		}
	}
	// Another account's sessions are not collateral damage.
	if _, err := s.Lookup(ctx, other.ID); err != nil {
		t.Errorf("another account's session was revoked: %v", err)
	}
}

// A password change signs the account out everywhere *else*: logging someone out of the
// tab they are typing in is not a security win.
func TestRevokeAll_KeepsTheNamedSession(t *testing.T) {
	ctx := context.Background()
	s := newStore()
	keep, _ := s.Create(ctx, 5)
	drop, _ := s.Create(ctx, 5)

	if err := s.RevokeAll(ctx, 5, keep.ID); err != nil {
		t.Fatalf("RevokeAll: %v", err)
	}
	if _, err := s.Lookup(ctx, keep.ID); err != nil {
		t.Errorf("the kept session was revoked: %v", err)
	}
	if _, err := s.Lookup(ctx, drop.ID); !errors.Is(err, session.ErrInvalidSession) {
		t.Errorf("another session survived: %v", err)
	}
}

// A session opened after a bulk revocation carries the new epoch, so it must not be
// invalidated by the revocation that came before it.
func TestCreate_AfterRevokeAllStaysValid(t *testing.T) {
	ctx := context.Background()
	s := newStore()
	if err := s.RevokeAll(ctx, 3, ""); err != nil {
		t.Fatalf("RevokeAll: %v", err)
	}
	sess, err := s.Create(ctx, 3)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Lookup(ctx, sess.ID); err != nil {
		t.Fatalf("new session was born revoked: %v", err)
	}
}
