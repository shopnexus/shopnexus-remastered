package mock_test

import (
	"context"
	"testing"

	"shopnexus/internal/provider"
	finance "shopnexus/internal/provider/payment"
	financemock "shopnexus/internal/provider/payment/mock"
)

func TestCharge_Success(t *testing.T) {
	c := financemock.NewClient(provider.Option{Provider: "mock"})
	res, err := c.Charge(context.Background(), finance.ChargeParams{RefID: "r1", Amount: 500})
	if err != nil {
		t.Fatalf("Charge: %v", err)
	}
	if res.Status != finance.StatusSuccess || res.ProviderID == "" {
		t.Fatalf("unexpected charge result: %+v", res)
	}
}

func TestRefund_Success(t *testing.T) {
	c := financemock.NewClient(provider.Option{Provider: "mock"})
	res, err := c.Refund(context.Background(), finance.RefundParams{ProviderChargeID: "p1", Amount: 500})
	if err != nil {
		t.Fatalf("Refund: %v", err)
	}
	if res.Status != finance.StatusSuccess || res.ProviderRefundID == "" {
		t.Fatalf("unexpected refund result: %+v", res)
	}
}
