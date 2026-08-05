package finance_test

import (
	"context"
	"testing"

	"shopnexus/internal/module/common"
	financeapi "shopnexus/internal/module/finance/api"
	"shopnexus/internal/shared/id"
)

func TestListPaymentOptions_IsWhereAValidSlugComesFrom(t *testing.T) {
	h := newHarness("user", true)

	rails, err := h.svc.ListPaymentOptions(context.Background(), financeapi.ListPaymentOptionsRequest{
		ActorID: buyer,
	})
	if err != nil {
		t.Fatalf("ListPaymentOptions: %v", err)
	}
	if len(rails) != 1 || rails[0].ID != "mock-rail" {
		t.Fatalf("rails = %+v, want the one enabled row", rails)
	}
	// The list is what a client tenders with, so what it answers has to be accepted.
	if _, err := h.svc.StartPayment(context.Background(), financeapi.StartPaymentRequest{
		ActorID: buyer, ID: openSession(t, h), PaymentOption: rails[0].ID,
	}); err != nil {
		t.Fatalf("StartPayment with a listed rail: %v", err)
	}
}

// A row naming a vendor this deployment is not configured for is a claim the database cannot keep:
// the rail would be handed a charge it has never heard of. It is not listed, and tendering it is
// refused with the same 422 as a slug nobody enabled — the two are the same fact to a client.
func TestPaymentOptions_ARailThisDeploymentCannotReachIsNotOffered(t *testing.T) {
	h := newHarness("user", true)
	h.repo.options = append(h.repo.options, common.Option{
		ID: "vnpay-qr", Name: "VNPay QR", Type: common.OptionTypePayment,
		IsEnabled: true, Provider: "vnpay",
	})

	rails, err := h.svc.ListPaymentOptions(context.Background(), financeapi.ListPaymentOptionsRequest{
		ActorID: buyer,
	})
	if err != nil {
		t.Fatalf("ListPaymentOptions: %v", err)
	}
	for _, r := range rails {
		if r.ID == "vnpay-qr" {
			t.Fatal("a rail for another vendor is offered, so a checkout can pick one nothing can charge")
		}
	}
	if got := status(t, mustErr(h.svc.StartPayment(context.Background(), financeapi.StartPaymentRequest{
		ActorID: buyer, ID: openSession(t, h), PaymentOption: "vnpay-qr",
	}))); got != 422 {
		t.Fatalf("status = %d, want 422", got)
	}
}

// openSession is a checkout to tender against. Its own helper because both tests here need one and
// neither is about opening it.
func openSession(t *testing.T, h *harness) id.ID[id.PaymentSession] {
	t.Helper()
	s, err := h.svc.OpenCheckout(context.Background(), financeapi.OpenCheckoutRequest{
		BuyerID: buyer, SellerID: seller, Currency: "VND", Total: 300_000, Note: "Ao thun",
	})
	if err != nil {
		t.Fatalf("OpenCheckout: %v", err)
	}
	return s.ID
}
