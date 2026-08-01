package mock_test

import (
	"context"
	"testing"

	"shopnexus/internal/provider/payment"
	paymentmock "shopnexus/internal/provider/payment/mock"
)

// The rail decides on the spot, which is what makes a local checkout complete without a webhook.
func TestCharge_Success(t *testing.T) {
	c := paymentmock.NewClient()
	res, err := c.Charge(context.Background(), payment.ChargeParams{RefID: "r1", Amount: 500})
	if err != nil {
		t.Fatalf("Charge: %v", err)
	}
	if res.Status != payment.StatusSuccess || res.ProviderID == "" {
		t.Fatalf("unexpected charge result: %+v", res)
	}
	if res.RedirectURL != "" {
		t.Errorf("redirect = %q, want none from a direct-debit rail", res.RedirectURL)
	}
}
