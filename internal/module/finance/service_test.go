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
	"shopnexus/internal/provider/payment"
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
	gateway := paymentmock.NewClient()
	svc := finance.NewService(repo, fakeAccounts{role: role, verified: verified}, repo,
		gateway, finance.ReturnURLHosts{"shopnexus.test"}, eventbus.NewMemory(slog.New(slog.DiscardHandler)), validation.Default(), slog.New(slog.DiscardHandler))
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

// seedBalance is a top-up by hand, for a test that needs money without walking a checkout.
func seedBalance(t *testing.T, h *harness, amount int64) {
	t.Helper()
	adminH := newHarnessSharing(h, "admin")
	_, err := adminH.svc.AdminAdjustWallet(context.Background(), financeapi.AdjustWalletRequest{
		ActorID: admin, AccountID: buyer, Currency: "VND", AvailableDelta: amount,
		Reason: "test seed", IdempotencyKey: "seed:" + t.Name(),
	})
	if err != nil {
		t.Fatalf("seed balance: %v", err)
	}
}

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
		Reason: "test top-up", IdempotencyKey: "seed-1",
	}); err == nil {
		t.Fatal("a plain user adjusted a wallet")
	}
	adminH := newHarnessSharing(h, "admin")
	if _, err := adminH.svc.AdminAdjustWallet(ctx, financeapi.AdjustWalletRequest{
		ActorID: admin, AccountID: buyer, Currency: "VND", AvailableDelta: 500_000,
		Reason: "test top-up", IdempotencyKey: "seed-2",
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
		Reason: "test top-up", IdempotencyKey: "seed-3",
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
		paymentmock.NewClient(),
		finance.ReturnURLHosts{"shopnexus.test"}, eventbus.NewMemory(slog.New(slog.DiscardHandler)),
		validation.Default(), slog.New(slog.DiscardHandler))
	return &harness{svc: svc, repo: h.repo}
}

// The money has to become a balance, or the escrow hold that follows has nothing to move.
// This is the whole checkout path in one test: a session, a rail payment, then the hold the
// order module makes the moment it hears the session was paid.
func TestSettledCheckout_CreditsThePayerSoEscrowCanHold(t *testing.T) {
	h := newHarness("user", true)
	ctx := context.Background()

	session, err := h.svc.OpenCheckout(ctx, financeapi.OpenCheckoutRequest{
		BuyerID: buyer, SellerID: seller, Currency: "VND", Total: 300_000, Note: "Ao thun",
	})
	if err != nil {
		t.Fatalf("OpenCheckout: %v", err)
	}
	// The buyer's wallet is empty: nobody topped it up, which is the ordinary case.
	if _, err := h.svc.StartPayment(ctx, financeapi.StartPaymentRequest{
		ActorID: buyer, ID: session.ID, PaymentOption: "mock-rail",
	}); err != nil {
		t.Fatalf("StartPayment: %v", err)
	}
	wallet, err := h.svc.GetWallet(ctx, financeapi.GetWalletRequest{
		ActorID: buyer, AccountID: buyer, Currency: "VND",
	})
	if err != nil {
		t.Fatalf("GetWallet: %v", err)
	}
	if wallet.AvailableBalance != 300_000 {
		t.Fatalf("available = %d, want the rail money credited", wallet.AvailableBalance)
	}
	// Which is what makes the hold possible. Without the credit this is a 409 and the sale
	// ends up with no escrow behind it.
	if err := h.svc.HoldEscrow(ctx, financeapi.EscrowRequest{
		BuyerID: buyer, SellerID: seller, OrderID: id.Of[id.Order](77),
		Currency: "VND", Amount: 300_000, IdempotencyKey: "order:77:hold",
	}); err != nil {
		t.Fatalf("HoldEscrow: %v", err)
	}
	sellerWallet, err := h.svc.GetWallet(ctx, financeapi.GetWalletRequest{
		ActorID: seller, AccountID: seller, Currency: "VND",
	})
	if err != nil {
		t.Fatalf("GetWallet: %v", err)
	}
	if sellerWallet.HeldBalance != 300_000 {
		t.Fatalf("held = %d, want the escrow on the seller", sellerWallet.HeldBalance)
	}
	// And a redelivered notification credits once: the topup carries the session as its key.
	if err := h.svc.Settle(ctx, paymentNotification(t, h, session.ID)); err != nil {
		t.Fatalf("redelivered Settle: %v", err)
	}
	wallet, err = h.svc.GetWallet(ctx, financeapi.GetWalletRequest{
		ActorID: buyer, AccountID: buyer, Currency: "VND",
	})
	if err != nil {
		t.Fatalf("GetWallet: %v", err)
	}
	if wallet.AvailableBalance != 0 || wallet.HeldBalance != 0 {
		t.Fatalf("buyer wallet = %+v, want the money moved once and now in escrow", wallet)
	}
}

// paymentNotification rebuilds what the rail sends back for a session's only leg, which is
// how a redelivery is tested without a webhook.
func paymentNotification(t *testing.T, h *harness, sessionID id.ID[id.PaymentSession]) payment.Notification {
	t.Helper()
	legs, err := h.svc.ListSessionTransactions(context.Background(), financeapi.GetSessionRequest{
		ActorID: buyer, ID: sessionID,
	})
	if err != nil {
		t.Fatalf("ListSessionTransactions: %v", err)
	}
	if len(legs) != 1 {
		t.Fatalf("legs = %d, want the one the payment opened", len(legs))
	}
	return payment.Notification{RefID: legs[0].ID.String(), Status: payment.StatusSuccess}
}

// A withdrawal shares the payment-session id space and names its requester as the payer, so
// without a kind guard the requester could drive their own cash-out to `success` through the
// checkout route — or cancel it here, where nothing gives the debited money back.
func TestWithdrawalSession_IsNotPayableOrCancellableAsACheckout(t *testing.T) {
	h := newHarness("user", true)
	ctx := context.Background()
	seedBalance(t, h, 500_000)

	payee, err := h.svc.CreateBankAccount(ctx, financeapi.CreateBankAccountRequest{
		ActorID: buyer, BankCode: "vcb", AccountNumber: "0123456789", AccountHolder: "NGUYEN VAN A",
	})
	if err != nil {
		t.Fatalf("CreateBankAccount: %v", err)
	}
	cashout, err := h.svc.CreateWithdrawal(ctx, financeapi.CreateWithdrawalRequest{
		ActorID: buyer, BankAccountID: payee.ID, Currency: "VND", Amount: 200_000,
	})
	if err != nil {
		t.Fatalf("CreateWithdrawal: %v", err)
	}

	if got := status(t, mustErr(h.svc.StartPayment(ctx, financeapi.StartPaymentRequest{
		ActorID: buyer, ID: cashout.ID, PaymentOption: "mock-rail",
	}))); got != 409 {
		t.Fatalf("status = %d, want 409 tendering against a cash-out", got)
	}
	if got := status(t, mustErr(h.svc.CancelSession(ctx, financeapi.GetSessionRequest{
		ActorID: buyer, ID: cashout.ID,
	}))); got != 409 {
		t.Fatalf("status = %d, want 409 cancelling a cash-out here", got)
	}
	// The route that does own it returns the money.
	if err := h.svc.CancelWithdrawal(ctx, financeapi.WithdrawalRequest{ActorID: buyer, ID: cashout.ID}); err != nil {
		t.Fatalf("CancelWithdrawal: %v", err)
	}
	wallet, err := h.svc.GetWallet(ctx, financeapi.GetWalletRequest{
		ActorID: buyer, AccountID: buyer, Currency: "VND",
	})
	if err != nil {
		t.Fatalf("GetWallet: %v", err)
	}
	if wallet.AvailableBalance != 500_000 {
		t.Fatalf("available = %d, want the debit returned", wallet.AvailableBalance)
	}
}

// The one balance change with no business event behind it is also the one with nothing else
// to lose a replay to, so the key is what stops a double-clicked correction crediting twice.
func TestAdminAdjustWallet_KeyedAgainstAReplay(t *testing.T) {
	h := newHarnessSharing(newHarness("user", true), "admin")
	ctx := context.Background()
	req := financeapi.AdjustWalletRequest{
		ActorID: admin, AccountID: buyer, Currency: "VND", AvailableDelta: 250_000,
		Reason: "support credit", IdempotencyKey: "ticket-4021",
	}
	first, err := h.svc.AdminAdjustWallet(ctx, req)
	if err != nil {
		t.Fatalf("AdminAdjustWallet: %v", err)
	}
	if first.AvailableBalance != 250_000 {
		t.Fatalf("available = %d, want the credit", first.AvailableBalance)
	}
	again, err := h.svc.AdminAdjustWallet(ctx, req)
	if err != nil {
		t.Fatalf("replayed AdminAdjustWallet: %v", err)
	}
	if again.AvailableBalance != 250_000 {
		t.Fatalf("available = %d, want the replay to credit nothing more", again.AvailableBalance)
	}
	// And a request with no key at all is refused rather than posting unprotected.
	req.IdempotencyKey = ""
	if err := mustErr(h.svc.AdminAdjustWallet(ctx, req)); err == nil {
		t.Fatal("an adjustment with no idempotency key was accepted")
	}
}

// A return URL the platform does not redirect to is refused: unchecked it is an open redirect
// borrowing this domain's credibility.
func TestStartPayment_RefusesAForeignReturnURL(t *testing.T) {
	h := newHarness("user", true)
	ctx := context.Background()
	session, err := h.svc.OpenCheckout(ctx, financeapi.OpenCheckoutRequest{
		BuyerID: buyer, SellerID: seller, Currency: "VND", Total: 100_000,
	})
	if err != nil {
		t.Fatalf("OpenCheckout: %v", err)
	}
	if got := status(t, mustErr(h.svc.StartPayment(ctx, financeapi.StartPaymentRequest{
		ActorID: buyer, ID: session.ID, PaymentOption: "mock-rail",
		ReturnURL: "https://phishing.example/paid",
	}))); got != 400 {
		t.Fatalf("status = %d, want 400", got)
	}
	if _, err := h.svc.StartPayment(ctx, financeapi.StartPaymentRequest{
		ActorID: buyer, ID: session.ID, PaymentOption: "mock-rail",
		ReturnURL: "https://shopnexus.test/orders",
	}); err != nil {
		t.Fatalf("StartPayment with an allowed host: %v", err)
	}
}
