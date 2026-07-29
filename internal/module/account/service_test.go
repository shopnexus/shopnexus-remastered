package account_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"shopnexus/internal/module/account"
	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/id/idtest"
	"shopnexus/internal/shared/token"
)

func TestMain(m *testing.M) { idtest.Install(); m.Run() }

type fakeRepo struct {
	byEmail map[string]*domain.Account
	byID    map[int64]*domain.Account
	created *domain.Account
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{byEmail: map[string]*domain.Account{}, byID: map[int64]*domain.Account{}}
}

func (f *fakeRepo) Create(_ context.Context, a *domain.Account) error {
	a.ID = 1
	f.created = a
	f.byEmail[a.Email] = a
	f.byID[a.ID] = a
	return nil
}
func (f *fakeRepo) ExistsByEmail(_ context.Context, email string) (bool, error) {
	_, ok := f.byEmail[email]
	return ok, nil
}
func (f *fakeRepo) FindByEmail(_ context.Context, email string) (domain.Account, error) {
	a, ok := f.byEmail[email]
	if !ok {
		return domain.Account{}, domain.ErrAccountNotFound
	}
	return *a, nil
}
func (f *fakeRepo) FindByID(_ context.Context, accID int64) (domain.Account, error) {
	a, ok := f.byID[accID]
	if !ok {
		return domain.Account{}, domain.ErrAccountNotFound
	}
	return *a, nil
}

func newSvc(repo *fakeRepo) *account.Service {
	return account.NewService(repo, token.NewManager("0123456789012345678901234567890123", time.Hour), slog.Default())
}

func TestRegister_ThenLogin(t *testing.T) {
	repo := newFakeRepo()
	svc := newSvc(repo)

	p, err := svc.Register(context.Background(), accountapi.RegisterRequest{Email: "a@b.com", Password: "password1", Name: "Alice"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if p.ID != id.Of[id.Account](1) || p.DisplayName != "Alice" {
		t.Fatalf("unexpected profile: %+v", p)
	}

	tok, err := svc.Login(context.Background(), accountapi.LoginRequest{Email: "a@b.com", Password: "password1"})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if tok.AccessToken == "" {
		t.Fatal("expected access token")
	}
}

func TestLogin_WrongPasswordUnauthorized(t *testing.T) {
	repo := newFakeRepo()
	svc := newSvc(repo)
	_, _ = svc.Register(context.Background(), accountapi.RegisterRequest{Email: "a@b.com", Password: "password1", Name: "Alice"})

	_, err := svc.Login(context.Background(), accountapi.LoginRequest{Email: "a@b.com", Password: "wrongpass"})
	if status, _, _, ok := errx.Decompose(err); !ok || status != 401 {
		t.Fatalf("expected Unauthorized, got %v", err)
	}
}

func TestGetProfile_NotFound(t *testing.T) {
	svc := newSvc(newFakeRepo())
	_, err := svc.GetProfile(context.Background(), accountapi.GetProfileRequest{UserID: id.Of[id.Account](404)})
	if status, _, _, ok := errx.Decompose(err); !ok || status != 404 {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

func TestRegister_DuplicateEmailConflict(t *testing.T) {
	repo := newFakeRepo()
	svc := newSvc(repo)
	if _, err := svc.Register(context.Background(), accountapi.RegisterRequest{Email: "a@b.com", Password: "password1", Name: "Alice"}); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	_, err := svc.Register(context.Background(), accountapi.RegisterRequest{Email: "a@b.com", Password: "password2", Name: "Bob"})
	if status, _, _, ok := errx.Decompose(err); !ok || status != 409 {
		t.Fatalf("expected Conflict on duplicate email, got %v", err)
	}
}

func TestLogin_UnknownEmailUnauthorized(t *testing.T) {
	svc := newSvc(newFakeRepo())
	_, err := svc.Login(context.Background(), accountapi.LoginRequest{Email: "nobody@b.com", Password: "password1"})
	if status, _, _, ok := errx.Decompose(err); !ok || status != 401 {
		t.Fatalf("expected Unauthorized for unknown email, got %v", err)
	}
}

// Invariant: password must be hashed, never stored as plaintext.
func TestRegister_HashesPassword(t *testing.T) {
	repo := newFakeRepo()
	svc := newSvc(repo)
	if _, err := svc.Register(context.Background(), accountapi.RegisterRequest{Email: "a@b.com", Password: "password1", Name: "Alice"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if repo.created == nil {
		t.Fatal("expected account to be created")
	}
	if repo.created.PasswordHash == "" || repo.created.PasswordHash == "password1" {
		t.Fatalf("password must be hashed, got %q", repo.created.PasswordHash)
	}
}
