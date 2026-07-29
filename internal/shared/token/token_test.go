package token_test

import (
	"testing"
	"time"

	"shopnexus/internal/shared/token"
)

func TestIssueParse_RoundTrip(t *testing.T) {
	m := token.NewManager("0123456789012345678901234567890123", time.Hour)
	tok, err := m.Issue("user-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	uid, err := m.Parse(tok)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if uid != "user-1" {
		t.Fatalf("uid = %q, want user-1", uid)
	}
}

func TestParse_Invalid(t *testing.T) {
	m := token.NewManager("0123456789012345678901234567890123", time.Hour)
	if _, err := m.Parse("not-a-token"); err == nil {
		t.Fatal("expected error for invalid token")
	}
}

func TestParse_Expired(t *testing.T) {
	// Negative TTL -> token is already expired at issue time.
	m := token.NewManager("0123456789012345678901234567890123", -time.Hour)
	tok, err := m.Issue("user-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := m.Parse(tok); err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestParse_WrongSecret(t *testing.T) {
	a := token.NewManager("0123456789012345678901234567890123", time.Hour)
	b := token.NewManager("XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX", time.Hour)
	tok, err := a.Issue("user-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := b.Parse(tok); err == nil {
		t.Fatal("expected error for token signed with a different secret")
	}
}
