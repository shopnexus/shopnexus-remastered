//go:build integration

package postgres_test

import (
	"context"
	"os"
	"testing"
	"time"

	"shopnexus/internal/infra/postgres"
	pgadapter "shopnexus/internal/module/account/adapter/postgres"
	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/module/account/port"
)

// These exercise the SQL a fake cannot: the identifier uniqueness, the geography column,
// the token-keyed device upsert, and the hypertable's keyset read. They skip when no DSN is
// set, so `go test ./...` stays database-free.

func newRepo(t *testing.T) *pgadapter.Repo {
	t.Helper()
	dsn := os.Getenv("ACCOUNT_DB_DSN")
	if dsn == "" {
		t.Skip("ACCOUNT_DB_DSN not set")
	}
	pool, err := postgres.NewPool(context.Background(), dsn, "account")
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pgadapter.New(pool)
}

// unique keeps parallel runs and repeated runs against the same database apart.
func unique(prefix string) string {
	return prefix + time.Now().Format("150405.000000000")
}

func createAccount(t *testing.T, repo *pgadapter.Repo) domain.Account {
	t.Helper()
	acc, err := domain.NewAccount(domain.RoleUser, unique("it-")+"@test.local", "", "", "hash")
	if err != nil {
		t.Fatalf("NewAccount: %v", err)
	}
	profile, err := domain.NewProfile("Integration", "VN", "vi-VN", "Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatalf("NewProfile: %v", err)
	}
	if err := repo.CreateAccount(context.Background(), &acc, &profile); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	return acc
}

func TestRepo_CreateAccountWritesProfileToo(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	acc := createAccount(t, repo)

	got, err := repo.FindAccountByIdentifier(ctx, acc.Email)
	if err != nil {
		t.Fatalf("FindAccountByIdentifier: %v", err)
	}
	if got.ID != acc.ID || got.Email != acc.Email {
		t.Fatalf("account = %+v, want %+v", got, acc)
	}
	profile, err := repo.FindProfile(ctx, acc.ID)
	if err != nil {
		t.Fatalf("FindProfile: %v", err)
	}
	if profile.Name != "Integration" {
		t.Fatalf("profile = %+v", profile)
	}
}

// The UNIQUE columns are what the API's 409 rests on, so the adapter has to translate the
// constraint violation rather than leak a driver error.
func TestRepo_DuplicateIdentifierIsConflict(t *testing.T) {
	repo := newRepo(t)
	first := createAccount(t, repo)

	dup, _ := domain.NewAccount(domain.RoleUser, first.Email, "", "", "hash")
	profile, _ := domain.NewProfile("Dup", "VN", "vi-VN", "Asia/Ho_Chi_Minh")
	if err := repo.CreateAccount(context.Background(), &dup, &profile); err != domain.ErrIdentifierTaken {
		t.Fatalf("err = %v, want ErrIdentifierTaken", err)
	}
}

// The coordinate goes into a geography column and has to come back as the two numbers the
// API speaks.
func TestRepo_ContactRoundTripsTheCoordinate(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	acc := createAccount(t, repo)

	lat, lng := 10.7769, 106.7009
	c := domain.Contact{
		AccountID: acc.ID, FullName: "Nguyễn A", Phone: "+84901234567",
		AddressType: domain.AddressTypeHome, IsDefaultDelivery: true,
		Country: "VN", ProvinceCode: "79", ProvinceName: "TP HCM",
		WardCode: "26734", WardName: "Bến Nghé", Address: "1 Lê Lợi",
		Latitude: &lat, Longitude: &lng,
	}
	if err := repo.InsertContact(ctx, &c); err != nil {
		t.Fatalf("InsertContact: %v", err)
	}
	got, err := repo.FindContact(ctx, c.ID)
	if err != nil {
		t.Fatalf("FindContact: %v", err)
	}
	if got.Latitude == nil || got.Longitude == nil {
		t.Fatalf("contact = %+v, want the coordinate back", got)
	}
	// geography stores float8, so compare with a tolerance rather than for equality.
	if diff := *got.Latitude - lat; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("latitude = %v, want %v", *got.Latitude, lat)
	}
}

// One default per role per account: claiming the default has to move it, not fail on the
// partial unique index.
func TestRepo_SecondDefaultContactMovesTheFlag(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	acc := createAccount(t, repo)

	base := domain.Contact{
		AccountID: acc.ID, FullName: "A", Phone: "+84901234567", AddressType: domain.AddressTypeHome,
		IsDefaultDelivery: true, Country: "VN", ProvinceCode: "79", ProvinceName: "TP HCM",
		WardCode: "26734", WardName: "Bến Nghé", Address: "1 Lê Lợi",
	}
	first := base
	if err := repo.InsertContact(ctx, &first); err != nil {
		t.Fatalf("first InsertContact: %v", err)
	}
	second := base
	second.Address = "2 Lê Lợi"
	if err := repo.InsertContact(ctx, &second); err != nil {
		t.Fatalf("second InsertContact: %v", err)
	}

	got, err := repo.FindContact(ctx, first.ID)
	if err != nil {
		t.Fatalf("FindContact: %v", err)
	}
	if got.IsDefaultDelivery {
		t.Error("the previous default was not cleared")
	}
}

// The push token identifies an install, so registering it under another account moves the
// row instead of creating a second one.
func TestRepo_UpsertDeviceMovesTheRowBetweenAccounts(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	first, second := createAccount(t, repo), createAccount(t, repo)
	pushToken := unique("push-")

	a, err := domain.NewDevice(first.ID, domain.PlatformIOS, pushToken)
	if err != nil {
		t.Fatalf("NewDevice: %v", err)
	}
	if err := repo.UpsertDevice(ctx, &a); err != nil {
		t.Fatalf("first UpsertDevice: %v", err)
	}
	b, _ := domain.NewDevice(second.ID, domain.PlatformAndroid, pushToken)
	if err := repo.UpsertDevice(ctx, &b); err != nil {
		t.Fatalf("second UpsertDevice: %v", err)
	}
	if b.ID != a.ID {
		t.Errorf("device id = %d, want the same row %d", b.ID, a.ID)
	}
	if devices, err := repo.ListDevices(ctx, first.ID); err != nil || len(devices) != 0 {
		t.Errorf("the previous owner still has the device: %v %v", devices, err)
	}
}

// Preferences are sparse: a deviation is stored, and a pair back at its default is deleted.
func TestRepo_SavePreferencesStoresAndDeletes(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	acc := createAccount(t, repo)
	pref := domain.Preference{AccountID: acc.ID, Category: domain.CategoryOrder, Channel: domain.ChannelPush, IsEnabled: false}

	if err := repo.SavePreferences(ctx, acc.ID, []domain.Preference{pref}, nil); err != nil {
		t.Fatalf("SavePreferences: %v", err)
	}
	stored, err := repo.ListPreferences(ctx, acc.ID)
	if err != nil || len(stored) != 1 {
		t.Fatalf("stored = %+v, err = %v", stored, err)
	}
	if err := repo.SavePreferences(ctx, acc.ID, nil, []domain.Preference{pref}); err != nil {
		t.Fatalf("SavePreferences (delete): %v", err)
	}
	if stored, err = repo.ListPreferences(ctx, acc.ID); err != nil || len(stored) != 0 {
		t.Fatalf("stored = %+v, err = %v; want the row deleted", stored, err)
	}
}

// The feed is a hypertable read by a keyset bound, and the query has to work with no bound
// at all as well.
func TestRepo_ListNotificationsAcceptsNoCursor(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	acc := createAccount(t, repo)

	if _, err := repo.ListNotifications(ctx, port.NotificationQuery{AccountID: acc.ID, Limit: 20}); err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	if _, err := repo.ListNotifications(ctx, port.NotificationQuery{
		AccountID: acc.ID, Category: domain.CategoryOrder, UnreadOnly: true, Before: time.Now(), Limit: 20,
	}); err != nil {
		t.Fatalf("ListNotifications (filtered): %v", err)
	}
	if n, err := repo.CountUnreadNotifications(ctx, acc.ID); err != nil || n != 0 {
		t.Fatalf("unread = %d, err = %v", n, err)
	}
}

// The audit log derives its version from the rows already there, so two entries for the
// same record come out 1 and 2 rather than colliding.
func TestRepo_AuditLogVersionsPerRecord(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	acc := createAccount(t, repo)

	for i := 0; i < 2; i++ {
		err := repo.InsertAuditLog(ctx, port.AuditEntry{
			Table: "account", RecordID: acc.ID, ChangeType: "update", Code: "account.suspend",
			ChangedBy: acc.ID, Diff: map[string]any{"status": "suspended"},
			Snapshot: map[string]any{"id": acc.ID},
		})
		if err != nil {
			t.Fatalf("InsertAuditLog #%d: %v", i+1, err)
		}
	}
}

// The admin search is one query for both shapes: an exact identifier and a name fragment.
func TestRepo_SearchAccountsMatchesIdentifierAndName(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	acc := createAccount(t, repo)

	byEmail, total, err := repo.SearchAccounts(ctx, port.AccountFilter{Query: acc.Email, Limit: 10})
	if err != nil {
		t.Fatalf("SearchAccounts (email): %v", err)
	}
	if total == 0 || len(byEmail) == 0 || byEmail[0].ID != acc.ID {
		t.Fatalf("rows = %+v, total = %d", byEmail, total)
	}
	byName, _, err := repo.SearchAccounts(ctx, port.AccountFilter{Query: "Integrat", Limit: 10})
	if err != nil {
		t.Fatalf("SearchAccounts (name fragment): %v", err)
	}
	if len(byName) == 0 {
		t.Fatal("a display-name fragment matched nothing")
	}
}
