package domain_test

import (
	"testing"

	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/shared/errx"
)

func TestNewAccount_Valid(t *testing.T) {
	a, err := domain.NewAccount("a@b.com", "Alice", "hash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.Email != "a@b.com" || a.Name != "Alice" || a.PasswordHash != "hash" {
		t.Fatalf("unexpected account: %+v", a)
	}
}

func TestNewAccount_EmptyEmailInvalid(t *testing.T) {
	_, err := domain.NewAccount("", "Alice", "hash")
	if status, _, _, ok := errx.Decompose(err); !ok || status != 400 {
		t.Fatalf("expected Invalid errx, got %v", err)
	}
}

func TestNewAccount_EmptyNameInvalid(t *testing.T) {
	_, err := domain.NewAccount("a@b.com", "", "hash")
	if status, _, _, ok := errx.Decompose(err); !ok || status != 400 {
		t.Fatalf("expected Invalid errx, got %v", err)
	}
}

func TestNewAccount_EmptyPasswordHashInvalid(t *testing.T) {
	_, err := domain.NewAccount("a@b.com", "Alice", "")
	if status, _, _, ok := errx.Decompose(err); !ok || status != 400 {
		t.Fatalf("expected Invalid errx, got %v", err)
	}
}
