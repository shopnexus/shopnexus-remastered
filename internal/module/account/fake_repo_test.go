package account_test

import (
	"context"
	"slices"
	"sort"
	"strings"
	"time"

	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/module/account/port"
)

// fakeRepo is an in-memory port.Repository. It exists so the service's rules can be tested
// without a database, and it enforces the constraints the schema does — the unique
// identifiers, the version check, one default contact per role, one live verified document
// per account — because those are the ones the service's behaviour is built on top of.
type fakeRepo struct {
	nextID int64

	// accounts holds the row — display half included, as in the table. The links live
	// beside it, so nothing the test holds aliases what is stored.
	accounts  map[int64]domain.Account
	oauth     map[int64][]domain.OAuthIdentity
	contacts  map[int64]domain.Contact
	devices   map[int64]domain.Device
	notifs    []domain.Notification
	prefs     map[int64][]domain.Preference
	follows   map[[2]int64]time.Time
	documents map[int64]domain.IdentityDocument
	audit     []port.AuditEntry
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		accounts:  map[int64]domain.Account{},
		oauth:     map[int64][]domain.OAuthIdentity{},
		contacts:  map[int64]domain.Contact{},
		devices:   map[int64]domain.Device{},
		prefs:     map[int64][]domain.Preference{},
		follows:   map[[2]int64]time.Time{},
		documents: map[int64]domain.IdentityDocument{},
	}
}

func (f *fakeRepo) id() int64 {
	f.nextID++
	return f.nextID
}

var _ port.Repository = (*fakeRepo)(nil)

// --- the account aggregate ---

func (f *fakeRepo) Create(_ context.Context, a *domain.Account) error {
	if err := a.Validate(); err != nil {
		return err
	}
	if err := f.identifiersFree(a, 0); err != nil {
		return err
	}
	a.ID = f.id()
	a.Version = 1
	a.CreatedAt = time.Now()
	a.Profile.ID = a.ID
	f.putRow(a)
	f.syncIdentities(a)
	a.ClearEvents()
	return nil
}

func (f *fakeRepo) Get(_ context.Context, id int64) (*domain.Account, error) {
	row, ok := f.accounts[id]
	if !ok {
		return nil, domain.ErrAccountNotFound
	}
	return f.hydrate(row), nil
}

func (f *fakeRepo) GetByIdentifier(ctx context.Context, identifier string) (*domain.Account, error) {
	for _, a := range f.accounts {
		if takenBy(a.Email, identifier) || takenBy(a.Phone, identifier) || takenBy(a.Username, identifier) {
			return f.Get(ctx, a.ID)
		}
	}
	return nil, domain.ErrAccountNotFound
}

func (f *fakeRepo) GetByEmail(ctx context.Context, email string) (*domain.Account, error) {
	for _, a := range f.accounts {
		if takenBy(a.Email, email) {
			return f.Get(ctx, a.ID)
		}
	}
	return nil, domain.ErrAccountNotFound
}

func (f *fakeRepo) GetByOAuth(ctx context.Context, provider, uid string) (*domain.Account, error) {
	for accountID, links := range f.oauth {
		for _, l := range links {
			if l.Provider == provider && l.ProviderUID == uid {
				return f.Get(ctx, accountID)
			}
		}
	}
	return nil, domain.ErrOAuthIdentityNotFound
}

// Save mirrors the adapter: validate, take the version, write the row and the links, then
// the trail for whatever the root recorded.
func (f *fakeRepo) Save(_ context.Context, a *domain.Account, actor int64) error {
	if err := a.Validate(); err != nil {
		return err
	}
	stored, ok := f.accounts[a.ID]
	if !ok || stored.Version != a.Version {
		return domain.ErrVersionConflict
	}
	if err := f.identifiersFree(a, a.ID); err != nil {
		return err
	}
	f.putRow(a)
	f.syncIdentities(a)
	var changedBy *int64
	if actor != 0 {
		changedBy = &actor
	}
	row := f.accounts[a.ID]
	row.Version++
	f.accounts[a.ID] = row
	// As the adapter does: the version is bumped before the trail, so the snapshot describes
	// the row as it now is.
	a.Version++
	snapshot := a.Snapshot()
	for _, e := range a.Events() {
		f.audit = append(f.audit, port.AuditEntry{
			Table: "account", RecordID: a.ID, ChangeType: "update", Code: string(e.Code),
			ChangedBy: changedBy, Diff: e.Payload, Snapshot: snapshot,
		})
	}
	a.ClearEvents()
	return nil
}

// hydrate builds an aggregate out of the stored row, cloning the links so a caller that
// never saves cannot change what is stored.
func (f *fakeRepo) hydrate(row domain.Account) *domain.Account {
	acc := row
	acc.Identities = nil
	for _, l := range f.oauth[row.ID] {
		acc.Identities = append(acc.Identities, &domain.OAuthIdentity{
			ID: l.ID, AccountID: l.AccountID, Provider: l.Provider,
			ProviderUID: l.ProviderUID, CreatedAt: l.CreatedAt,
		})
	}
	return &acc
}

// putRow stores the row without its links, so the oauth map stays the single source.
func (f *fakeRepo) putRow(a *domain.Account) {
	row := *a
	row.Identities = nil
	f.accounts[a.ID] = row
}

// syncIdentities is the adapter's delete-by-negation: the table matches the slice.
func (f *fakeRepo) syncIdentities(a *domain.Account) {
	kept := make([]domain.OAuthIdentity, 0, len(a.Identities))
	for _, i := range a.Identities {
		if i.ID == 0 {
			i.AccountID = a.ID
			i.ID = f.id()
			i.CreatedAt = time.Now()
		}
		kept = append(kept, *i)
	}
	f.oauth[a.ID] = kept
}

// identifiersFree mirrors the UNIQUE columns, skipping the account being written.
func (f *fakeRepo) identifiersFree(a *domain.Account, self int64) error {
	for id, other := range f.accounts {
		if id == self {
			continue
		}
		if taken(other.Email, a.Email) || taken(other.Phone, a.Phone) || taken(other.Username, a.Username) {
			return domain.ErrIdentifierTaken
		}
	}
	return nil
}

// taken mirrors the UNIQUE columns: two NULLs do not collide, two equal values do.
func taken(a, b *string) bool { return a != nil && b != nil && *a == *b }

// takenBy is the same check against a plain lookup value.
func takenBy(a *string, b string) bool { return a != nil && *a == b }

func (f *fakeRepo) SearchAccounts(_ context.Context, filter port.AccountFilter) ([]port.AccountSummary, int64, error) {
	var matched []port.AccountSummary
	for _, a := range f.accounts {
		if filter.Status != "" && a.Status != filter.Status {
			continue
		}
		if filter.Role != "" && a.Role != filter.Role {
			continue
		}
		if filter.Query != "" && !f.matchesQuery(a, filter.Query) {
			continue
		}
		matched = append(matched, port.AccountSummary{
			ID: a.ID, Status: a.Status, Role: a.Role, Email: a.Email, Phone: a.Phone,
			Username: a.Username, Name: a.Profile.Name, EmailVerified: a.EmailVerified,
			SuspendedUntil: a.SuspendedUntil, SuspensionReason: a.SuspensionReason,
			CreatedAt: a.CreatedAt,
		})
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].ID < matched[j].ID })
	total := int64(len(matched))
	if filter.Offset >= len(matched) {
		return nil, total, nil
	}
	end := min(filter.Offset+filter.Limit, len(matched))
	return matched[filter.Offset:end], total, nil
}

// matchesQuery is the adapter's "exact identifier or display-name fragment", in one place
// so a test exercises the same choice the SQL makes.
func (f *fakeRepo) matchesQuery(a domain.Account, q string) bool {
	if takenBy(a.Email, q) || takenBy(a.Phone, q) || takenBy(a.Username, q) {
		return true
	}
	return strings.Contains(strings.ToLower(a.Profile.Name), strings.ToLower(q))
}

// --- profile ---

func (f *fakeRepo) FindProfile(_ context.Context, accountID int64) (domain.Profile, error) {
	a, ok := f.accounts[accountID]
	if !ok {
		return domain.Profile{}, domain.ErrAccountNotFound
	}
	return a.Profile, nil
}

func (f *fakeRepo) FindProfiles(_ context.Context, ids []int64) (map[int64]domain.Profile, error) {
	out := map[int64]domain.Profile{}
	for _, id := range ids {
		if a, ok := f.accounts[id]; ok {
			out[id] = a.Profile
		}
	}
	return out, nil
}

func (f *fakeRepo) HasLiveVerifiedDocument(_ context.Context, accountID int64) (bool, error) {
	for _, d := range f.documents {
		if d.AccountID == accountID && d.IsLive(time.Now()) {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeRepo) LiveVerifiedDocuments(ctx context.Context, ids []int64) (map[int64]bool, error) {
	out := map[int64]bool{}
	for _, id := range ids {
		live, err := f.HasLiveVerifiedDocument(ctx, id)
		if err != nil {
			return nil, err
		}
		if live {
			out[id] = true
		}
	}
	return out, nil
}

func (f *fakeRepo) CountFollowers(_ context.Context, accountID int64) (int64, error) {
	var n int64
	for edge := range f.follows {
		if edge[1] == accountID {
			n++
		}
	}
	return n, nil
}

// --- saved addresses ---

func (f *fakeRepo) InsertContact(_ context.Context, c *domain.Contact) error {
	if _, ok := f.accounts[c.AccountID]; !ok {
		return domain.ErrAccountNotFound
	}
	c.ID = f.id()
	c.CreatedAt = time.Now()
	f.clearDefaults(*c)
	f.contacts[c.ID] = *c
	return nil
}

func (f *fakeRepo) UpdateContact(_ context.Context, c domain.Contact) error {
	current, ok := f.contacts[c.ID]
	if !ok || current.AccountID != c.AccountID {
		return domain.ErrContactNotFound
	}
	f.clearDefaults(c)
	f.contacts[c.ID] = c
	return nil
}

func (f *fakeRepo) DeleteContact(_ context.Context, accountID, contactID int64) error {
	c, ok := f.contacts[contactID]
	if !ok || c.AccountID != accountID {
		return domain.ErrContactNotFound
	}
	delete(f.contacts, contactID)
	return nil
}

func (f *fakeRepo) FindContact(_ context.Context, accountID, contactID int64) (domain.Contact, error) {
	c, ok := f.contacts[contactID]
	if !ok || c.AccountID != accountID {
		return domain.Contact{}, domain.ErrContactNotFound
	}
	return c, nil
}

func (f *fakeRepo) ListContacts(_ context.Context, accountID int64) ([]domain.Contact, error) {
	var out []domain.Contact
	for _, c := range f.contacts {
		if c.AccountID == accountID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// clearDefaults is the partial unique index: one default per role per account.
func (f *fakeRepo) clearDefaults(c domain.Contact) {
	for id, other := range f.contacts {
		if id == c.ID || other.AccountID != c.AccountID {
			continue
		}
		if c.IsDefaultDelivery {
			other.IsDefaultDelivery = false
		}
		if c.IsDefaultPickup {
			other.IsDefaultPickup = false
		}
		f.contacts[id] = other
	}
}

// --- push devices ---

func (f *fakeRepo) UpsertDevice(_ context.Context, d *domain.Device) error {
	// Keyed on the push token, so the row moves to whoever signed in last.
	for id, existing := range f.devices {
		if existing.PushToken == d.PushToken {
			existing.AccountID, existing.Platform = d.AccountID, d.Platform
			existing.LastSeenAt = time.Now()
			f.devices[id] = existing
			*d = existing
			return nil
		}
	}
	d.ID = f.id()
	d.CreatedAt, d.LastSeenAt = time.Now(), time.Now()
	f.devices[d.ID] = *d
	return nil
}

func (f *fakeRepo) FindDevice(_ context.Context, id int64) (domain.Device, error) {
	d, ok := f.devices[id]
	if !ok {
		return domain.Device{}, domain.ErrDeviceNotFound
	}
	return d, nil
}

func (f *fakeRepo) ListDevices(_ context.Context, accountID int64) ([]domain.Device, error) {
	var out []domain.Device
	for _, d := range f.devices {
		if d.AccountID == accountID {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *fakeRepo) DeleteDevice(_ context.Context, id int64) error {
	if _, ok := f.devices[id]; !ok {
		return domain.ErrDeviceNotFound
	}
	delete(f.devices, id)
	return nil
}

// --- notifications ---

func (f *fakeRepo) ListNotifications(_ context.Context, q port.NotificationQuery) ([]domain.Notification, error) {
	var out []domain.Notification
	for _, n := range f.notifs {
		switch {
		case n.AccountID != q.AccountID,
			q.Category != "" && n.Category != q.Category,
			q.UnreadOnly && n.ReadAt != nil,
			!q.Before.IsZero() && !n.CreatedAt.Before(q.Before):
			continue
		}
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

func (f *fakeRepo) CountUnreadNotifications(_ context.Context, accountID int64) (int64, error) {
	var n int64
	for _, x := range f.notifs {
		if x.AccountID == accountID && x.ReadAt == nil {
			n++
		}
	}
	return n, nil
}

func (f *fakeRepo) MarkNotificationsRead(_ context.Context, accountID int64, before *time.Time) error {
	now := time.Now()
	for i, n := range f.notifs {
		if n.AccountID != accountID || n.ReadAt != nil {
			continue
		}
		if before != nil && n.CreatedAt.After(*before) {
			continue
		}
		f.notifs[i].ReadAt = &now
	}
	return nil
}

func (f *fakeRepo) ListPreferences(_ context.Context, accountID int64) ([]domain.Preference, error) {
	return f.prefs[accountID], nil
}

func (f *fakeRepo) SavePreferences(_ context.Context, accountID int64, store, remove []domain.Preference) error {
	kept := f.prefs[accountID]
	drop := func(p domain.Preference) {
		for i, existing := range kept {
			if existing.Category == p.Category && existing.Channel == p.Channel {
				kept = append(kept[:i:i], kept[i+1:]...)
				return
			}
		}
	}
	for _, p := range store {
		drop(p)
		kept = append(kept, p)
	}
	for _, p := range remove {
		drop(p)
	}
	f.prefs[accountID] = kept
	return nil
}

// --- follow graph ---

func (f *fakeRepo) InsertFollow(_ context.Context, followerID, followeeID int64) error {
	if _, ok := f.accounts[followeeID]; !ok {
		return domain.ErrAccountNotFound
	}
	f.follows[[2]int64{followerID, followeeID}] = time.Now()
	return nil
}

func (f *fakeRepo) DeleteFollow(_ context.Context, followerID, followeeID int64) error {
	delete(f.follows, [2]int64{followerID, followeeID})
	return nil
}

func (f *fakeRepo) ListFollowing(_ context.Context, accountID int64, offset, limit int) ([]domain.Profile, int64, error) {
	return f.followPage(accountID, 0, offset, limit)
}

func (f *fakeRepo) ListFollowers(_ context.Context, accountID int64, offset, limit int) ([]domain.Profile, int64, error) {
	return f.followPage(accountID, 1, offset, limit)
}

// followPage walks the edges from one side: side 0 means "accounts I follow", side 1 means
// "accounts that follow me".
func (f *fakeRepo) followPage(accountID int64, side int, offset, limit int) ([]domain.Profile, int64, error) {
	var ids []int64
	for edge := range f.follows {
		if edge[side] == accountID {
			ids = append(ids, edge[1-side])
		}
	}
	slices.Sort(ids)
	total := int64(len(ids))
	if offset >= len(ids) {
		return nil, total, nil
	}
	var out []domain.Profile
	for _, id := range ids[offset:min(offset+limit, len(ids))] {
		out = append(out, f.accounts[id].Profile)
	}
	return out, total, nil
}

// --- payout identity verification ---

func (f *fakeRepo) InsertIdentityDocument(_ context.Context, d *domain.IdentityDocument) error {
	if _, ok := f.accounts[d.AccountID]; !ok {
		return domain.ErrAccountNotFound
	}
	d.ID = f.id()
	d.CreatedAt = time.Now()
	f.documents[d.ID] = *d
	return nil
}

func (f *fakeRepo) FindIdentityDocument(_ context.Context, id int64) (domain.IdentityDocument, error) {
	d, ok := f.documents[id]
	if !ok {
		return domain.IdentityDocument{}, domain.ErrIdentityDocumentNotFound
	}
	return d, nil
}

func (f *fakeRepo) ListIdentityDocuments(_ context.Context, accountID int64) ([]domain.IdentityDocument, error) {
	var out []domain.IdentityDocument
	for _, d := range f.documents {
		if d.AccountID == accountID {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

func (f *fakeRepo) ListIdentityDocumentsByStatus(_ context.Context, s domain.IdentityStatus, offset, limit int) ([]domain.IdentityDocument, int64, error) {
	var matched []domain.IdentityDocument
	for _, d := range f.documents {
		if d.Status == s {
			matched = append(matched, d)
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].ID < matched[j].ID })
	total := int64(len(matched))
	if offset >= len(matched) {
		return nil, total, nil
	}
	return matched[offset:min(offset+limit, len(matched))], total, nil
}

func (f *fakeRepo) UpdateIdentityVerdict(_ context.Context, d domain.IdentityDocument) error {
	current, ok := f.documents[d.ID]
	if !ok {
		return domain.ErrIdentityDocumentNotFound
	}
	// Only a pending row takes a verdict, as in the adapter's WHERE clause.
	if current.Status != domain.IdentityPending {
		return domain.ErrIdentityAlreadyDecided
	}
	if d.Status == domain.IdentityVerified {
		for _, other := range f.documents {
			if other.ID != d.ID && other.AccountID == d.AccountID && other.Status == domain.IdentityVerified {
				return domain.ErrIdentityAlreadyVerified
			}
		}
	}
	f.documents[d.ID] = d
	return nil
}

// --- audit ---

func (f *fakeRepo) InsertAuditLog(_ context.Context, e port.AuditEntry) error {
	f.audit = append(f.audit, e)
	return nil
}

// auditedDiff finds the payload recorded under one event type, at that type. A test that
// asserts on the trail should not be reaching into a map to do it.
func auditedDiff[T any](f *fakeRepo, e domain.EventType[T]) (T, bool) {
	for _, entry := range f.audit {
		if entry.Code != string(e.Code) {
			continue
		}
		if payload, ok := entry.Diff.(T); ok {
			return payload, true
		}
	}
	var zero T
	return zero, false
}

// codes lists the audit codes written so far, which is what a test asserts on.
func (f *fakeRepo) codes() []string {
	out := make([]string, 0, len(f.audit))
	for _, e := range f.audit {
		out = append(out, e.Code)
	}
	return out
}
