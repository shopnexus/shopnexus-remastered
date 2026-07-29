package domain_test

import (
	"testing"

	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/shared/errx"
)

func TestNewListing_Valid(t *testing.T) {
	l, err := domain.NewListing(7, "Bàn gỗ", 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if l.Status != domain.StatusActive {
		t.Fatalf("status = %q, want active", l.Status)
	}
}

func TestNewListing_InvalidPrice(t *testing.T) {
	_, err := domain.NewListing(7, "Bàn gỗ", 0)
	if status, _, _, ok := errx.Decompose(err); !ok || status != 400 {
		t.Fatalf("expected Invalid, got %v", err)
	}
}

func TestNewListing_ZeroOwner(t *testing.T) {
	_, err := domain.NewListing(0, "Bàn gỗ", 500)
	if status, _, _, ok := errx.Decompose(err); !ok || status != 400 {
		t.Fatalf("expected Invalid, got %v", err)
	}
}

func TestNewListing_EmptyTitle(t *testing.T) {
	_, err := domain.NewListing(7, "", 500)
	if status, _, _, ok := errx.Decompose(err); !ok || status != 400 {
		t.Fatalf("expected Invalid, got %v", err)
	}
}
