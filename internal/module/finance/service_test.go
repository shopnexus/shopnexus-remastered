package finance_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"shopnexus/internal/module/finance"
	financeapi "shopnexus/internal/module/finance/api"
	"shopnexus/internal/module/finance/domain"
	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/id/idtest"
)

func TestMain(m *testing.M) { idtest.Install(); m.Run() }

type fakeRepo struct {
	inserted *domain.Session
	wallet   domain.Wallet
	findErr  error
}

func (f *fakeRepo) InsertSession(_ context.Context, s *domain.Session) error {
	f.inserted = s
	return nil
}

func (f *fakeRepo) NextSessionID(_ context.Context) (int64, error) { return 77, nil }

func (f *fakeRepo) FindSessionByID(_ context.Context, sid int64) (domain.Session, error) {
	if f.findErr != nil {
		return domain.Session{}, f.findErr
	}
	return domain.Session{ID: sid, Kind: domain.KindBuyerCheckout, Status: domain.StatusPending, Currency: "VND", TotalAmount: 500}, nil
}

func (f *fakeRepo) FindWallet(_ context.Context, _ int64, _ string) (domain.Wallet, error) {
	return f.wallet, nil
}

func TestCreateSession(t *testing.T) {
	repo := &fakeRepo{}
	svc := finance.NewService(repo, slog.Default())
	got, err := svc.CreateSession(context.Background(), financeapi.CreateSessionRequest{
		Kind: domain.KindBuyerCheckout, FromID: id.Of[id.Account](11), Currency: "VND", TotalAmount: 500,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if got.ID != id.Of[id.PaymentSession](77) {
		t.Errorf("session id = %v, want the one drawn from the sequence", got.ID)
	}
	if got.Status != domain.StatusPending {
		t.Errorf("status = %q, want pending", got.Status)
	}
	if !got.ExpiredAt.After(time.Now()) {
		t.Errorf("expired_at = %v, want a future deadline", got.ExpiredAt)
	}
	if string(repo.inserted.Data) != "{}" {
		t.Errorf("data = %q, want {}", repo.inserted.Data)
	}
}

func TestCreateSession_RejectsBadCurrencyAndAmount(t *testing.T) {
	svc := finance.NewService(&fakeRepo{}, slog.Default())
	cases := map[string]financeapi.CreateSessionRequest{
		"currency too long": {Kind: domain.KindBuyerCheckout, Currency: "VNDD", TotalAmount: 1},
		"zero amount":       {Kind: domain.KindBuyerCheckout, Currency: "VND", TotalAmount: 0},
		"unknown kind":      {Kind: "gift", Currency: "VND", TotalAmount: 1},
	}
	for name, req := range cases {
		if _, err := svc.CreateSession(context.Background(), req); err == nil {
			t.Errorf("%s: expected an error", name)
		} else if status, _, _, ok := errx.Decompose(err); !ok || status != 400 {
			t.Errorf("%s: expected 400, got %v", name, err)
		}
	}
}

func TestGetWallet(t *testing.T) {
	repo := &fakeRepo{wallet: domain.Wallet{AccountID: 1, Currency: "VND", AvailableBalance: 900, HeldBalance: 100}}
	svc := finance.NewService(repo, slog.Default())
	got, err := svc.GetWallet(context.Background(), financeapi.GetWalletRequest{AccountID: id.Of[id.Account](1), Currency: "VND"})
	if err != nil {
		t.Fatalf("GetWallet: %v", err)
	}
	if got.AvailableBalance != 900 || got.HeldBalance != 100 {
		t.Fatalf("unexpected wallet: %+v", got)
	}
}

func TestGetSession_PropagatesNotFound(t *testing.T) {
	repo := &fakeRepo{findErr: domain.ErrSessionNotFound}
	svc := finance.NewService(repo, slog.Default())
	_, err := svc.GetSession(context.Background(), financeapi.GetSessionRequest{ID: id.Of[id.PaymentSession](404)})
	if status, _, _, ok := errx.Decompose(err); !ok || status != 404 {
		t.Fatalf("expected 404 through the wrap, got %v", err)
	}
}

func TestWallet_CanSpend(t *testing.T) {
	w := domain.Wallet{AvailableBalance: 100}
	if !w.CanSpend(100) {
		t.Error("CanSpend(100) = false, want true")
	}
	if w.CanSpend(101) {
		t.Error("CanSpend(101) = true, want false")
	}
	if w.CanSpend(0) {
		t.Error("CanSpend(0) = true, want false")
	}
}
