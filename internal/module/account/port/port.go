// Package port: the interface the account adapter must satisfy.
//
// It speaks in raw int64 keys and domain entities. Opaque ids stop at the api
// boundary, and the service is the only place that composes several of these calls
// into one answer — the repository returns entities and never a join-shaped row,
// so a queue listing resolves its subjects with one extra lookup instead of a
// second SQL shape that has to be kept in step with the first.
package port

import (
	"context"
	"time"

	"shopnexus/internal/module/account/domain"
)

// AccountFilter drives the admin search. Query is matched exactly against the
// unique identifiers *and* as a fragment against the display name, in one query.
type AccountFilter struct {
	Query  string
	Status domain.Status
	Role   domain.Role
	Offset int
	Limit  int
}

// NotificationQuery reads one page of the feed. Before is the keyset cursor —
// strictly older than this instant — and is zero on the first page; it is also the
// time bound that lets the hypertable exclude chunks.
type NotificationQuery struct {
	AccountID  int64
	Category   domain.Category
	UnreadOnly bool
	Before     time.Time
	Limit      int
}

// AuditEntry is one row of the module's audit log: the trail a suspension, a
// reinstatement or a role change leaves behind, since the account row itself
// carries only the state in force.
type AuditEntry struct {
	Table      string
	RecordID   int64
	ChangeType string
	// Code is the business event, e.g. "account.suspend".
	Code      string
	ChangedBy int64
	Diff      map[string]any
	Snapshot  map[string]any
}

type Repository interface {
	// --- account ---
	// CreateAccount inserts the account and its profile in one transaction, so an
	// account without a display name is never a reachable state.
	CreateAccount(ctx context.Context, a *domain.Account, p *domain.Profile) error
	FindAccountByID(ctx context.Context, id int64) (domain.Account, error)
	// FindAccountByIdentifier matches an email, a phone or a username — the sign-in
	// lookup, where the caller does not say which kind it sent.
	FindAccountByIdentifier(ctx context.Context, identifier string) (domain.Account, error)
	FindAccountByEmail(ctx context.Context, email string) (domain.Account, error)
	UpdateAccountIdentifiers(ctx context.Context, a domain.Account) error
	UpdateAccountPassword(ctx context.Context, accountID int64, passwordHash string) error
	MarkEmailVerified(ctx context.Context, accountID int64) error
	UpdateAccountStatus(ctx context.Context, a domain.Account) error
	UpdateAccountRole(ctx context.Context, accountID int64, role domain.Role) error
	SearchAccounts(ctx context.Context, f AccountFilter) ([]domain.Account, int64, error)

	// --- profile ---
	FindProfile(ctx context.Context, accountID int64) (domain.Profile, error)
	// FindProfiles resolves many at once, for the summaries in a follower list or the
	// identity review queue.
	FindProfiles(ctx context.Context, accountIDs []int64) (map[int64]domain.Profile, error)
	UpdateProfile(ctx context.Context, p domain.Profile) error

	// --- the cross-table facts an account view needs ---
	HasLiveVerifiedDocument(ctx context.Context, accountID int64) (bool, error)
	LiveVerifiedDocuments(ctx context.Context, accountIDs []int64) (map[int64]bool, error)
	CountFollowers(ctx context.Context, accountID int64) (int64, error)

	// --- federated identities ---
	FindOAuthIdentity(ctx context.Context, provider, providerUID string) (domain.OAuthIdentity, error)
	InsertOAuthIdentity(ctx context.Context, i *domain.OAuthIdentity) error
	ListOAuthIdentities(ctx context.Context, accountID int64) ([]domain.OAuthIdentity, error)
	DeleteOAuthIdentity(ctx context.Context, accountID int64, provider string) error
	CountOAuthIdentities(ctx context.Context, accountID int64) (int64, error)

	// --- saved addresses ---
	// InsertContact and UpdateContact own the "one default per role" rule: a row that
	// claims a default clears the previous one in the same transaction, because the
	// partial unique index would otherwise reject the write.
	InsertContact(ctx context.Context, c *domain.Contact) error
	UpdateContact(ctx context.Context, c domain.Contact) error
	FindContact(ctx context.Context, id int64) (domain.Contact, error)
	ListContacts(ctx context.Context, accountID int64) ([]domain.Contact, error)
	DeleteContact(ctx context.Context, id int64) error

	// --- push devices ---
	// UpsertDevice keys on the push token alone, not on (account, token): the token
	// identifies an install, so the row moves to whoever signed in last.
	UpsertDevice(ctx context.Context, d *domain.Device) error
	FindDevice(ctx context.Context, id int64) (domain.Device, error)
	ListDevices(ctx context.Context, accountID int64) ([]domain.Device, error)
	DeleteDevice(ctx context.Context, id int64) error

	// --- notifications ---
	ListNotifications(ctx context.Context, q NotificationQuery) ([]domain.Notification, error)
	CountUnreadNotifications(ctx context.Context, accountID int64) (int64, error)
	MarkNotificationsRead(ctx context.Context, accountID int64, before *time.Time) error
	ListPreferences(ctx context.Context, accountID int64) ([]domain.Preference, error)
	// SavePreferences applies one change set: store the deviations, delete the pairs
	// that went back to their default.
	SavePreferences(ctx context.Context, accountID int64, store, remove []domain.Preference) error

	// --- follow graph ---
	InsertFollow(ctx context.Context, followerID, followeeID int64) error
	DeleteFollow(ctx context.Context, followerID, followeeID int64) error
	ListFollowing(ctx context.Context, accountID int64, offset, limit int) ([]domain.Profile, int64, error)
	ListFollowers(ctx context.Context, accountID int64, offset, limit int) ([]domain.Profile, int64, error)

	// --- payout identity verification ---
	InsertIdentityDocument(ctx context.Context, d *domain.IdentityDocument) error
	FindIdentityDocument(ctx context.Context, id int64) (domain.IdentityDocument, error)
	ListIdentityDocuments(ctx context.Context, accountID int64) ([]domain.IdentityDocument, error)
	ListIdentityDocumentsByStatus(ctx context.Context, status domain.IdentityStatus, offset, limit int) ([]domain.IdentityDocument, int64, error)
	UpdateIdentityVerdict(ctx context.Context, d domain.IdentityDocument) error

	// --- audit ---
	InsertAuditLog(ctx context.Context, e AuditEntry) error
}
