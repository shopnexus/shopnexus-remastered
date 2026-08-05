package finance_test

import (
	"context"
	"testing"

	"shopnexus/internal/module/common"
	financeapi "shopnexus/internal/module/finance/api"
	paymentmock "shopnexus/internal/provider/payment/mock"
	"shopnexus/internal/shared/id"
)

func TestListOptions_IsWhereAValidSlugComesFrom(t *testing.T) {
	h := newHarness("user", true)

	list, err := h.svc.ListOptions(context.Background(), common.ListOptionsRequest{
		ActorID: buyer, Category: common.CategoryPayment,
	})
	if err != nil {
		t.Fatalf("ListOptions: %v", err)
	}
	if len(list.Options) != 1 || list.Options[0].ID != "mock-rail" {
		t.Fatalf("options = %+v, want the one enabled row", list.Options)
	}
	// The provider a rail runs on is a map of who this platform pays, and the list a buyer reads is
	// not where it belongs.
	if list.Options[0].Provider != "" || list.Options[0].IsEnabled != nil || list.Providers != nil {
		t.Errorf("staff fields leaked to a user: %+v / providers %v", list.Options[0], list.Providers)
	}
	// What the list answers has to be tenderable, or the list is not the source of the slug.
	if _, err := h.svc.StartPayment(context.Background(), financeapi.StartPaymentRequest{
		ActorID: buyer, ID: openSession(t, h), PaymentOption: list.Options[0].ID,
	}); err != nil {
		t.Fatalf("StartPayment with a listed rail: %v", err)
	}
}

// A row whose provider no implementation answers to is refused rather than charged through whichever
// rail happened to be registered: the money would move on a gateway nobody chose.
func TestStartPayment_ARailWithNoRegisteredProviderIsRefused(t *testing.T) {
	h := newHarness("user", true)
	h.repo.options = append(h.repo.options, common.Option{
		ID: "vnpay-qr", Name: "VNPay QR", Category: common.CategoryPayment,
		IsEnabled: true, Provider: "vnpay",
	})

	if got := status(t, mustErr(h.svc.StartPayment(context.Background(), financeapi.StartPaymentRequest{
		ActorID: buyer, ID: openSession(t, h), PaymentOption: "vnpay-qr",
	}))); got != 422 {
		t.Fatalf("status = %d, want 422", got)
	}
}

// The staff view is the same rows with the operator's half attached, and it includes what is switched
// off — "why is this rail missing from checkout" being the question it exists to answer.
func TestAdminListOptions_ShowsTheProviderAndTheDisabledRows(t *testing.T) {
	h := newHarness("admin", true)
	h.repo.options = append(h.repo.options, common.Option{
		ID: "retired-rail", Name: "Retired", Category: common.CategoryPayment, Provider: paymentmock.Name,
	})

	list, err := h.svc.ListOptions(context.Background(), common.ListOptionsRequest{
		ActorID: buyer, Category: common.CategoryPayment, Admin: true,
	})
	if err != nil {
		t.Fatalf("ListOptions: %v", err)
	}
	if len(list.Options) != 2 {
		t.Fatalf("options = %+v, want the disabled row too", list.Options)
	}
	for _, o := range list.Options {
		if o.Provider == "" || o.IsEnabled == nil {
			t.Errorf("row %q has no operator half: %+v", o.ID, o)
		}
	}
	// Without this an admin has to guess at a provider name to switch a rail to.
	if len(list.Providers) == 0 {
		t.Error("no providers offered, so a switch is a guess")
	}
}

// A user asking for the staff view gets the user view; asking for a category nobody defined gets the
// same 404 a staff-only category would — telling them apart enumerates the operator surface.
func TestListOptions_CategoryGate(t *testing.T) {
	h := newHarness("user", true)

	if got := status(t, mustErr(h.svc.ListOptions(context.Background(), common.ListOptionsRequest{
		ActorID: buyer, Category: common.CategoryPayment, Admin: true,
	}))); got != 403 {
		t.Errorf("admin view as a user = %d, want 403", got)
	}
	if got := status(t, mustErr(h.svc.ListOptions(context.Background(), common.ListOptionsRequest{
		ActorID: buyer, Category: "email",
	}))); got != 404 {
		t.Errorf("staff-only category as a user = %d, want 404", got)
	}
}

// Switching a rail to another implementation is one field, and the slug does not move with it: every
// settled payment naming it still means what it meant.
func TestAdminSaveOption_MovesARailBetweenProviders(t *testing.T) {
	h := newHarness("admin", true)

	if _, err := h.svc.AdminSaveOption(context.Background(), common.SaveOptionRequest{
		ActorID: buyer, ID: "mock-rail", Provider: new(paymentmock.Name),
	}); err != nil {
		t.Fatalf("AdminSaveOption: %v", err)
	}
	// A provider this binary does not have is refused, or the next payer meets a rail that cannot
	// charge them.
	if got := status(t, mustErr(h.svc.AdminSaveOption(context.Background(), common.SaveOptionRequest{
		ActorID: buyer, ID: "mock-rail", Provider: new("stripe"),
	}))); got != 422 {
		t.Errorf("unknown provider = %d, want 422", got)
	}
	// Disabling takes it out of the buyer's list without deleting the row every past leg names.
	if _, err := h.svc.AdminSaveOption(context.Background(), common.SaveOptionRequest{
		ActorID: buyer, ID: "mock-rail", IsEnabled: new(false),
	}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	user := newHarnessSharing(h, "user")
	list, err := user.svc.ListOptions(context.Background(), common.ListOptionsRequest{
		ActorID: buyer, Category: common.CategoryPayment,
	})
	if err != nil {
		t.Fatalf("ListOptions: %v", err)
	}
	if len(list.Options) != 0 {
		t.Fatalf("options = %+v, want the disabled rail gone", list.Options)
	}
}

// A non-admin editing the registry would be deciding who the platform's money goes through.
func TestAdminSaveOption_NeedsAnAdmin(t *testing.T) {
	h := newHarness("moderator", true)
	if got := status(t, mustErr(h.svc.AdminSaveOption(context.Background(), common.SaveOptionRequest{
		ActorID: buyer, ID: "mock-rail", IsEnabled: new(false),
	}))); got != 403 {
		t.Fatalf("status = %d, want 403", got)
	}
}

// SyncOptions is what makes a registered provider's rows exist, and what removes them when it stops
// being registered — so a scenario a client could pick never outlives the code behind it.
func TestSyncOptions_WritesWhatTheProviderDeclaresAndPrunesTheRest(t *testing.T) {
	h := newHarness("user", true)
	h.repo.options = append(h.repo.options, common.Option{
		ID: "mock-stale", Name: "Gone", Category: common.CategoryPayment,
		IsEnabled: true, Provider: paymentmock.Name,
	})

	if err := h.svc.SyncOptions(context.Background()); err != nil {
		t.Fatalf("SyncOptions: %v", err)
	}
	list, err := h.svc.ListOptions(context.Background(), common.ListOptionsRequest{
		ActorID: buyer, Category: common.CategoryPayment,
	})
	if err != nil {
		t.Fatalf("ListOptions: %v", err)
	}
	byID := map[string]bool{}
	for _, o := range list.Options {
		byID[o.ID] = true
	}
	// Both of these name the mock as their provider without being declared by it, so both go: a
	// provider that declares its rows owns every row pointing at it, which is what stops a scenario
	// from outliving the code that served it. An operator's own row belongs to a real provider.
	for _, gone := range []string{"mock-stale", "mock-rail"} {
		if byID[gone] {
			t.Errorf("%q survived, so a row nothing implements can still be tendered", gone)
		}
	}
	if !byID[paymentmock.OptionDecline] {
		t.Errorf("options = %+v, want every declared scenario", list.Options)
	}
}

// openSession is a checkout to tender against. Its own helper because several tests here need one and
// none of them is about opening it.
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

// Dropping a rail from PAYMENT_PROVIDERS is how it is taken out of service, and its rows have to go
// with it. Nothing deletes them — a bad deploy must not cost an operator their configuration — so
// the list is what has to leave them out: a row nobody can charge through is an error the payer can
// do nothing about, and in a deployment that takes real money the row it matters for is a mock's.
func TestListOptions_ARowNoRegisteredProviderServesIsNotOffered(t *testing.T) {
	h := newHarness("admin", true)
	h.repo.options = append(h.repo.options, common.Option{
		ID: "vnpay-qr", Name: "VNPay QR", Category: common.CategoryPayment,
		IsEnabled: true, Provider: "vnpay",
	})
	ctx := context.Background()

	user, err := newHarnessSharing(h, "user").svc.ListOptions(ctx, common.ListOptionsRequest{
		ActorID: buyer, Category: common.CategoryPayment,
	})
	if err != nil {
		t.Fatalf("ListOptions: %v", err)
	}
	for _, o := range user.Options {
		if o.ID == "vnpay-qr" {
			t.Fatal("a rail nothing can charge through is offered to a buyer")
		}
	}

	// Staff still see it, because "why is this missing from checkout" is the question the operator
	// view exists to answer — and the row carries the provider that explains it.
	staff, err := h.svc.ListOptions(ctx, common.ListOptionsRequest{
		ActorID: buyer, Category: common.CategoryPayment, Admin: true,
	})
	if err != nil {
		t.Fatalf("admin ListOptions: %v", err)
	}
	var found bool
	for _, o := range staff.Options {
		if o.ID == "vnpay-qr" {
			found = true
			if o.Provider != "vnpay" {
				t.Errorf("provider = %q, want the one nobody registered", o.Provider)
			}
		}
	}
	if !found {
		t.Error("the operator cannot see the row they have to fix")
	}
}
