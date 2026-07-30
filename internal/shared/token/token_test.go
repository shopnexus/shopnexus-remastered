package token_test

import (
	"testing"
	"time"

	"shopnexus/internal/shared/token"
)

const secret = "0123456789012345678901234567890123"

func TestIssueParse_RoundTrip(t *testing.T) {
	m := token.NewManager(secret, time.Hour)
	tok, err := m.Issue(token.Claims{AccountID: "acc_1", SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	got, err := m.Parse(tok)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.AccountID != "acc_1" || got.SessionID != "sess-1" {
		t.Fatalf("claims = %+v, want acc_1/sess-1", got)
	}
}

// The session id is what makes revocation possible, so a token without one is not a
// credential this API accepts: it would be valid until it expired and unrevocable.
func TestParse_MissingSessionRejected(t *testing.T) {
	m := token.NewManager(secret, time.Hour)
	tok, err := m.Issue(token.Claims{AccountID: "acc_1"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := m.Parse(tok); err == nil {
		t.Fatal("expected error for a token with no session id")
	}
}

func TestParse_Invalid(t *testing.T) {
	m := token.NewManager(secret, time.Hour)
	if _, err := m.Parse("not-a-token"); err == nil {
		t.Fatal("expected error for invalid token")
	}
}

func TestParse_Expired(t *testing.T) {
	// Negative TTL -> token is already expired at issue time.
	m := token.NewManager(secret, -time.Hour)
	tok, err := m.Issue(token.Claims{AccountID: "acc_1", SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := m.Parse(tok); err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestParse_WrongSecret(t *testing.T) {
	a := token.NewManager(secret, time.Hour)
	b := token.NewManager("XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX", time.Hour)
	tok, err := a.Issue(token.Claims{AccountID: "acc_1", SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := b.Parse(tok); err == nil {
		t.Fatal("expected error for token signed with a different secret")
	}
}

func TestTTL_IsWhatWasConfigured(t *testing.T) {
	if got := token.NewManager(secret, 15*time.Minute).TTL(); got != 15*time.Minute {
		t.Fatalf("TTL = %v, want 15m", got)
	}
}
