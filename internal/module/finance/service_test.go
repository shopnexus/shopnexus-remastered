package finance_test

import (
	"context"
	"log/slog"
	"testing"

	"shopnexus/internal/infra/eventbus"
	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/api/accounttest"
	"shopnexus/internal/module/finance"
	financeapi "shopnexus/internal/module/finance/api"
	"shopnexus/internal/module/finance/domain"
	"shopnexus/internal/provider"
	paymentmock "shopnexus/internal/provider/payment/mock"
	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/id/idtest"
	"shopnexus/internal/shared/validation"
)

func TestMain(m *testing.M) { idtest.Install(); m.Run() }

// fakeAccounts answers the two questions finance asks of the account module: the
// caller's role, and whether a payee's identity is verified.
type fakeAccounts struct {
	accounttest.Stub
	role     string
	verified bool
}

func (f fakeAccounts) GetMe(context.Context, accountapi.GetMeRequest) (accountapi.Me, error) {
	return accountapi.Me{Role: f.role}, nil
}

func (f fakeAccounts) GetPublicAccount(_ context.Context, req accountapi.GetPublicAccountRequest) (accountapi.PublicAccount, error) {
	return accountapi.PublicAccount{ID: req.ID, IdentityVerified: f.verified}, nil
}

type harness struct {
	svc  *finance.Service
	repo *fakeRepo
}

func newHarness(role string, verified bool) *harness {
	repo := newFakeRepo()
	// The mock rail settles synchronously, which is what lets a service test walk a
	// whole payment without a webhook.
	gateway := paymentmock.NewClient(provider.Option{Provider: "mock"})
	svc := finance.NewService(repo, fakeAccounts{role: role, verified: verified}, repo,
		gateway, eventbus.NewMemory(slog.New(slog.DiscardHandler)), validation.Default(), slog.New(slog.DiscardHandler))
	return &harness{svc: svc, repo: repo}
}

func status(t *testing.T, err error) uint16 {
	t.Helper()
	s, _, _, ok := errx.Decompose(err)
	if !ok {
		t.Fatalf("expected a coded error, got %v", err)
	}
	return s
}

func mustErr[T any](_ T, err error) error { return err }

const (
	buyer  = id.ID[id.Account](1)
	seller = id.ID[id.Account](2)
	admin  = id.ID[id.Account](3)
)

// The escrow round trip is the whole point of the module: the buyer's money leaves,
// arrives held on the seller's side, and only becomes theirs on release.
func TestEscrow_HoldThenRelease(t *testing.T) {
	h := newHarness("user", true)
	ctx := context.Background()
	// Give the buyer a balance to spend, the way a top-up would.
	if _, err := h.svc.AdminAdjustWallet(ctx, financeapi.AdjustWalletRequest{
		ActorID: admin, AccountID: buyer, Currency: "VND", AvailableDelta: 500_000,
		Reason: "test top-up",
	}); err == nil {
		t.Fatal("a plain user adjusted a wallet")
	}
	adminH := newHarnessSharing(h, "admin")
	if _, err := adminH.svc.AdminAdjustWallet(ctx, financeapi.AdjustWalletRequest{
		ActorID: admin, AccountID: buyer, Currency: "VND", AvailableDelta: 500_000,
		Reason: "test top-up",
	}); err != nil {
		t.Fatalf("AdminAdjustWallet: %v", err)
	}

	escrow := financeapi.EscrowRequest{
		BuyerID: buyer, SellerID: seller, OrderID: id.Of[id.Order](9),
		Currency: "VND", Amount: 300_000, IdempotencyKey: "order:9:hold",
	}
	if err := h.svc.HoldEscrow(ctx, escrow); err != nil {
		t.Fatalf("HoldEscrow: %v", err)
	}
	buyerWallet, err := h.svc.GetWallet(ctx, financeapi.GetWalletRequest{
		ActorID: buyer, AccountID: buyer, Currency: "VND",
	})
	if err != nil {
		t.Fatalf("GetWallet: %v", err)
	}
	if buyerWallet.AvailableBalance != 200_000 {
		t.Errorf("buyer available = %d, want 200000", buyerWallet.AvailableBalance)
	}
	sellerWallet, err := h.svc.GetWallet(ctx, financeapi.GetWalletRequest{
		ActorID: seller, AccountID: seller, Currency: "VND",
	})
	if err != nil {
		t.Fatalf("GetWallet: %v", err)
	}
	// Held, not available: paid for, but not the seller's to spend yet.
	if sellerWallet.HeldBalance != 300_000 || sellerWallet.AvailableBalance != 0 {
		t.Fatalf("seller wallet = %+v, want it all held", sellerWallet)
	}

	// The same hold again is refused rather than posted twice — that is what the
	// idempotency key is for, and a retried webhook is the reason it exists.
	if err := h.svc.HoldEscrow(ctx, escrow); status(t, err) != 409 {
		t.Fatalf("second HoldEscrow = %v, want a conflict", err)
	}

	release := escrow
	release.IdempotencyKey = "order:9:release"
	if err := h.svc.ReleaseEscrow(ctx, release); err != nil {
		t.Fatalf("ReleaseEscrow: %v", err)
	}
	sellerWallet, err = h.svc.GetWallet(ctx, financeapi.GetWalletRequest{
		ActorID: seller, AccountID: seller, Currency: "VND",
	})
	if err != nil {
		t.Fatalf("GetWallet: %v", err)
	}
	if sellerWallet.AvailableBalance != 300_000 || sellerWallet.HeldBalance != 0 {
		t.Fatalf("seller wallet = %+v, want it released", sellerWallet)
	}
}

// A refund takes it out of escrow and gives it back, which has to leave both wallets
// exactly where they started.
func TestEscrow_Refund(t *testing.T) {
	h := newHarness("user", true)
	adminH := newHarnessSharing(h, "admin")
	ctx := context.Background()
	if _, err := adminH.svc.AdminAdjustWallet(ctx, financeapi.AdjustWalletRequest{
		ActorID: admin, AccountID: buyer, Currency: "VND", AvailableDelta: 100_000,
		Reason: "test top-up",
	}); err != nil {
		t.Fatalf("AdminAdjustWallet: %v", err)
	}
	escrow := financeapi.EscrowRequest{
		BuyerID: buyer, SellerID: seller, OrderID: id.Of[id.Order](11),
		Currency: "VND", Amount: 100_000, IdempotencyKey: "order:11:hold",
	}
	if err := h.svc.HoldEscrow(ctx, escrow); err != nil {
		t.Fatalf("HoldEscrow: %v", err)
	}
	refund := escrow
	refund.IdempotencyKey = "order:11:refund"
	if err := h.svc.RefundEscrow(ctx, refund); err != nil {
		t.Fatalf("RefundEscrow: %v", err)
	}

	buyerWallet, _ := h.svc.GetWallet(ctx, financeapi.GetWalletRequest{
		ActorID: buyer, AccountID: buyer, Currency: "VND",
	})
	sellerWallet, _ := h.svc.GetWallet(ctx, financeapi.GetWalletRequest{
		ActorID: seller, AccountID: seller, Currency: "VND",
	})
	if buyerWallet.AvailableBalance != 100_000 {
		t.Errorf("buyer available = %d, want the money back", buyerWallet.AvailableBalance)
	}
	if sellerWallet.HeldBalance != 0 || sellerWallet.AvailableBalance != 0 {
		t.Errorf("seller wallet = %+v, want nothing left", sellerWallet)
	}
}

// Nothing may drive a balance below zero, whichever direction it is asked from.
func TestWallet_RefusesOverdraft(t *testing.T) {
	h := newHarness("user", true)
	ctx := context.Background()
	err := h.svc.HoldEscrow(ctx, financeapi.EscrowRequest{
		BuyerID: buyer, SellerID: seller, OrderID: id.Of[id.Order](12),
		Currency: "VND", Amount: 1, IdempotencyKey: "order:12:hold",
	})
	if got := status(t, err); got != 409 {
		t.Fatalf("status = %d, want 409 for an empty wallet", got)
	}
}

// A balance is the closest thing here to a bank statement: only its owner and an admin
// may read it.
func TestGetWallet_SomebodyElsesIsAdminOnly(t *testing.T) {
	h := newHarness("user", true)
	ctx := context.Background()
	err := mustErr(h.svc.GetWallet(ctx, financeapi.GetWalletRequest{
		ActorID: buyer, AccountID: seller, Currency: "VND",
	}))
	if got := status(t, err); got != 403 {
		t.Fatalf("status = %d, want 403", got)
	}
}

// The whole checkout: order opens a session, the buyer tenders a rail, and the mock
// settles it on the spot — which is what marks the session paid.
func TestCheckout_PaysThroughARail(t *testing.T) {
	h := newHarness("user", true)
	ctx := context.Background()
	session, err := h.svc.OpenCheckout(ctx, financeapi.OpenCheckoutRequest{
		BuyerID: buyer, SellerID: seller, Currency: "VND", Total: 250_000, Note: "order 1",
	})
	if err != nil {
		t.Fatalf("OpenCheckout: %v", err)
	}
	if session.Status != domain.StatusPending || session.Outstanding != 250_000 {
		t.Fatalf("session = %+v, want it pending and unpaid", session)
	}

	leg, err := h.svc.StartPayment(ctx, financeapi.StartPaymentRequest{
		ActorID: buyer, ID: session.ID, PaymentOption: "mock-rail",
	})
	if err != nil {
		t.Fatalf("StartPayment: %v", err)
	}
	// The mock rail is direct-debit: it answers final, so the leg is already settled.
	if leg.Status != domain.StatusSuccess {
		t.Fatalf("leg = %+v, want it settled by the synchronous rail", leg)
	}
	after, err := h.svc.GetSession(ctx, financeapi.GetSessionRequest{ActorID: buyer, ID: session.ID})
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if after.Status != domain.StatusSuccess || after.Outstanding != 0 {
		t.Fatalf("session = %+v, want it paid in full", after)
	}
}

// A rail nobody enabled is refused before a gateway is called: the registry is this
// module's own option rows, and a slug that is not in it is not a rail.
func TestStartPayment_UnknownRail(t *testing.T) {
	h := newHarness("user", true)
	ctx := context.Background()
	session, err := h.svc.OpenCheckout(ctx, financeapi.OpenCheckoutRequest{
		BuyerID: buyer, SellerID: seller, Currency: "VND", Total: 1000,
	})
	if err != nil {
		t.Fatalf("OpenCheckout: %v", err)
	}
	err = mustErr(h.svc.StartPayment(ctx, financeapi.StartPaymentRequest{
		ActorID: buyer, ID: session.ID, PaymentOption: "no-such-rail",
	}))
	if got := status(t, err); got != 422 {
		t.Fatalf("status = %d, want 422", got)
	}
}

// Somebody else's session is not found rather than forbidden — it is not theirs to know
// about, and a 403 would confirm it exists.
func TestGetSession_StrangerNotFound(t *testing.T) {
	h := newHarness("user", true)
	ctx := context.Background()
	session, err := h.svc.OpenCheckout(ctx, financeapi.OpenCheckoutRequest{
		BuyerID: buyer, SellerID: seller, Currency: "VND", Total: 1000,
	})
	if err != nil {
		t.Fatalf("OpenCheckout: %v", err)
	}
	err = mustErr(h.svc.GetSession(ctx, financeapi.GetSessionRequest{
		ActorID: id.Of[id.Account](99), ID: session.ID,
	}))
	if got := status(t, err); got != 404 {
		t.Fatalf("status = %d, want 404", got)
	}
}

// newHarnessSharing reuses one harness's repository with a different role, so a test can
// seed money as an admin and then act as the account it belongs to.
func newHarnessSharing(h *harness, role string) *harness {
	svc := finance.NewService(h.repo, fakeAccounts{role: role, verified: true}, h.repo,
		paymentmock.NewClient(provider.Option{Provider: "mock"}), eventbus.NewMemory(slog.New(slog.DiscardHandler)),
		validation.Default(), slog.New(slog.DiscardHandler))
	return &harness{svc: svc, repo: h.repo}
}
