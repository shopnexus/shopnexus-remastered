//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"shopnexus/internal/infra/postgres"
	pgadapter "shopnexus/internal/module/account/adapter/postgres"
	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/module/account/port"
	"shopnexus/internal/module/common"
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

func createAccount(t *testing.T, repo *pgadapter.Repo) *domain.Account {
	t.Helper()
	profile, err := domain.NewProfile("Integration", "VN", "vi-VN", "Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatalf("NewProfile: %v", err)
	}
	acc, err := domain.NewAccount(domain.RoleUser, unique("it-")+"@test.local", "", "", "hash", profile)
	if err != nil {
		t.Fatalf("NewAccount: %v", err)
	}
	if err := repo.Create(context.Background(), acc); err != nil {
		t.Fatalf("Create: %v", err)
	}
	return acc
}

func TestRepo_CreateWritesTheWholeAggregate(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	acc := createAccount(t, repo)

	got, err := repo.GetByIdentifier(ctx, *acc.Email)
	if err != nil {
		t.Fatalf("GetByIdentifier: %v", err)
	}
	if got.ID != acc.ID || got.Email == nil || *got.Email != *acc.Email {
		t.Fatalf("account = %+v, want %+v", got, acc)
	}
	if got.Profile.Name != "Integration" {
		t.Fatalf("profile = %+v", got.Profile)
	}
	if got.Version != 1 {
		t.Fatalf("version = %d, want 1", got.Version)
	}
}

// The UNIQUE columns are what the API's 409 rests on, so the adapter has to translate the
// constraint violation rather than leak a driver error.
func TestRepo_DuplicateIdentifierIsConflict(t *testing.T) {
	repo := newRepo(t)
	first := createAccount(t, repo)

	profile, _ := domain.NewProfile("Dup", "VN", "vi-VN", "Asia/Ho_Chi_Minh")
	dup, _ := domain.NewAccount(domain.RoleUser, *first.Email, "", "", "hash", profile)
	if err := repo.Create(context.Background(), dup); !errors.Is(err, domain.ErrIdentifierTaken) {
		t.Fatalf("err = %v, want ErrIdentifierTaken", err)
	}
}

// The coordinate goes into a geography column and has to come back as the two numbers the
// API speaks.
func TestRepo_ContactRoundTripsTheCoordinate(t *testing.T) {
	repo := newRepo(t)
	acc := createAccount(t, repo)

	lat, lng := 10.7769, 106.7009
	c := domain.Contact{
		AccountID: acc.ID, FullName: "Nguyễn A", Phone: "+84901234567",
		AddressType: domain.AddressTypeHome, IsDefaultDelivery: true,
		Country: "VN", ProvinceCode: "79", ProvinceName: "TP HCM",
		WardCode: "26734", WardName: "Bến Nghé", Address: "1 Lê Lợi",
		Latitude: &lat, Longitude: &lng,
	}
	c = addContact(t, repo, c)
	got := findContact(t, repo, acc.ID, c.ID)
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
	acc := createAccount(t, repo)

	base := domain.Contact{
		AccountID: acc.ID, FullName: "A", Phone: "+84901234567", AddressType: domain.AddressTypeHome,
		IsDefaultDelivery: true, Country: "VN", ProvinceCode: "79", ProvinceName: "TP HCM",
		WardCode: "26734", WardName: "Bến Nghé", Address: "1 Lê Lợi",
	}
	first := addContact(t, repo, base)
	second := base
	second.Address = "2 Lê Lợi"
	addContact(t, repo, second)

	got := findContact(t, repo, acc.ID, first.ID)
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
		err := repo.InsertAuditLog(ctx, common.AuditEntry{
			Table: "account", RecordID: acc.ID, ChangeType: "update",
			Code:      string(domain.Suspended.Code),
			ChangedBy: &acc.ID,
			Diff:      domain.StatusChange{Status: domain.StatusSuspended},
			Snapshot:  acc.Snapshot(),
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

	byEmail, total, err := repo.SearchAccounts(ctx, port.AccountFilter{Query: *acc.Email, Limit: 10})
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

func addContact(t *testing.T, repo *pgadapter.Repo, c domain.Contact) domain.Contact {
	t.Helper()
	if err := repo.InsertContact(context.Background(), &c); err != nil {
		t.Fatalf("InsertContact: %v", err)
	}
	return c
}

func findContact(t *testing.T, repo *pgadapter.Repo, accountID, contactID int64) domain.Contact {
	t.Helper()
	c, err := repo.FindContact(context.Background(), accountID, contactID)
	if err != nil {
		t.Fatalf("FindContact: %v", err)
	}
	return c
}

// Two commands built on the same read: the second one loses on the version check rather
// than overwriting what the first decided. This is what stops two concurrent unlinks of
// different providers from both finding "another way in" and leaving an account nobody can
// reach.
func TestRepo_SaveRefusesAStaleAggregate(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	acc := createAccount(t, repo)

	first, err := repo.Get(ctx, acc.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	second, err := repo.Get(ctx, acc.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, err := first.Link("google", unique("google-sub-")); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if err := repo.Save(ctx, first, first.ID); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	if _, err := second.Link("apple", unique("apple-sub-")); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if err := repo.Save(ctx, second, second.ID); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("second Save = %v, want ErrVersionConflict", err)
	}
	// And the winner's change is the one on disk, links included.
	got, err := repo.Get(ctx, acc.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Identities) != 1 || got.Identities[0].Provider != "google" {
		t.Fatalf("identities = %+v, want only the committed one", got.Identities)
	}
	if got.Version != first.Version {
		t.Errorf("version = %d, want %d", got.Version, first.Version)
	}
}

// Save synchronises the links to the slice, so unlinking is a delete with no removal list
// to keep — and the trail for it lands in the same transaction.
func TestRepo_SaveSyncsLinksAndWritesTheTrail(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	acc := createAccount(t, repo)

	if _, err := acc.Link("google", unique("google-sub-")); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if err := repo.Save(ctx, acc, acc.ID); err != nil {
		t.Fatalf("Save (link): %v", err)
	}
	if err := acc.Unlink("google"); err != nil {
		t.Fatalf("Unlink: %v", err)
	}
	if err := repo.Save(ctx, acc, acc.ID); err != nil {
		t.Fatalf("Save (unlink): %v", err)
	}
	got, err := repo.Get(ctx, acc.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Identities) != 0 {
		t.Fatalf("identities = %+v, want the row deleted", got.Identities)
	}
	if len(acc.Events()) != 0 {
		t.Error("Save left the events on the aggregate")
	}
}
