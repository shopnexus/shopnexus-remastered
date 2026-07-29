package domain_test

import (
	"testing"

	"shopnexus/internal/module/order/domain"
	"shopnexus/internal/shared/errx"
)

func TestNewOrder_Valid(t *testing.T) {
	o, err := domain.NewOrder(7, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if o.Status != domain.StatusPending {
		t.Fatalf("status = %q, want pending", o.Status)
	}
}

func TestNewOrder_InvalidTotal(t *testing.T) {
	_, err := domain.NewOrder(7, 0)
	if status, _, _, ok := errx.Decompose(err); !ok || status != 400 {
		t.Fatalf("expected Invalid, got %v", err)
	}
}

func TestNewOrder_ZeroBuyer(t *testing.T) {
	_, err := domain.NewOrder(0, 500)
	if status, _, _, ok := errx.Decompose(err); !ok || status != 400 {
		t.Fatalf("expected Invalid, got %v", err)
	}
}
