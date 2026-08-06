// Package port: the interface the account adapter must satisfy.
//
// It speaks in raw int64 keys and domain entities — opaque ids stop at the api boundary.
// The repository returns entities, never a join-shaped row, so the service is the one
// place that composes several calls into one answer.
package port

import (
	"context"
	"time"

	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/module/common"
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

// AccountSummary is the admin list row: a flat read model, so a page of twenty accounts
// is one query rather than twenty aggregate loads.
type AccountSummary struct {
	ID               int64
	Status           domain.Status
	Role             domain.Role
	Email            *string
	Phone            *string
	Username         *string
	Name             string
	EmailVerified    bool
	SuspendedUntil   *time.Time
	SuspensionReason *string
	CreatedAt        time.Time
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

type Repository interface {
	// --- the account aggregate: load it, change it in memory, save it ---

	// Create inserts the account with its profile in one transaction, so an account
	// without a display name is never a reachable state.
	Create(ctx context.Context, a *domain.Account) error
	// Get reads the root with its children, and is the only loader — which is what makes
	// exported children and a state-based Save safe.
	Get(ctx context.Context, id int64) (*domain.Account, error)
	// GetByIdentifier matches an email, a phone or a username: the sign-in lookup, where
	// the caller does not say which kind it sent.
	GetByIdentifier(ctx context.Context, identifier string) (*domain.Account, error)
	GetByEmail(ctx context.Context, email string) (*domain.Account, error)
	// GetSupportAccount reads the support desk's own row — the second side of every ticket thread —
	// by its role, which is the one thing about it a user cannot register. A deployment that never
	// seeded it answers domain.ErrSupportAccountMissing rather than some other account.
	GetSupportAccount(ctx context.Context) (*domain.Account, error)
	// GetByOAuth is the one account read that cannot start from an id.
	GetByOAuth(ctx context.Context, provider, providerUID string) (*domain.Account, error)
	// Save validates the aggregate and writes the root, its links and the audit rows for
	// what it recorded in one transaction, guarded by Version. A stale copy gets
	// domain.ErrVersionConflict rather than overwriting somebody else's change.
	//
	// actor is who is performing the write, for those audit rows — a fact about this
	// transaction rather than about the account, which is why it is an argument and not a
	// field somebody has to remember to set. Zero means no account is responsible: a
	// scheduled job, a vendor callback.
	Save(ctx context.Context, a *domain.Account, actor int64) error

	// --- read models: flat rows, no children ---
	SearchAccounts(ctx context.Context, f AccountFilter) ([]AccountSummary, int64, error)
	// FindProfile is for a caller that wants only the profile — the locale a notification
	// is written in, not the account behind it.
	FindProfile(ctx context.Context, accountID int64) (domain.Profile, error)
	FindProfiles(ctx context.Context, accountIDs []int64) (map[int64]domain.Profile, error)

	// --- the cross-table facts an account view needs ---
	HasLiveVerifiedDocument(ctx context.Context, accountID int64) (bool, error)
	LiveVerifiedDocuments(ctx context.Context, accountIDs []int64) (map[int64]bool, error)
	CountFollowers(ctx context.Context, accountID int64) (int64, error)
	// IsFollowing is the reader's own relationship to the account being read, which is what
	// lets a follow button render its state instead of guessing.
	IsFollowing(ctx context.Context, followerID, followeeID int64) (bool, error)

	// --- saved addresses: their own aggregate, scoped by the owner rather than reached
	// through one. The one-default-per-kind rule is a partial unique index, which the
	// adapter clears the previous holder for in the same transaction.
	InsertContact(ctx context.Context, c *domain.Contact) error
	UpdateContact(ctx context.Context, c domain.Contact) error
	DeleteContact(ctx context.Context, accountID, contactID int64) error
	FindContact(ctx context.Context, accountID, contactID int64) (domain.Contact, error)
	ListContacts(ctx context.Context, accountID int64) ([]domain.Contact, error)

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
	// InsertNotification writes one feed row and answers its generated id. The
	// caller has already decided the account wants it in-app: preference is a
	// service rule, not a storage one.
	InsertNotification(ctx context.Context, n domain.Notification) (int64, error)
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
	InsertAuditLog(ctx context.Context, e common.AuditEntry) error
}
