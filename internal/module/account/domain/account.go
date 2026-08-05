// Package domain: the account entities and the rules that hold whatever calls them.
package domain

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/validation"
)

type Status string

const (
	StatusActive    Status = "active"
	StatusSuspended Status = "suspended"
)

// Role separates a user from the staff who moderate them, and both from the support desk.
// There is no seller role: the same account buys and sells.
type Role string

const (
	RoleUser      Role = "user"
	RoleModerator Role = "moderator"
	RoleAdmin     Role = "admin"
	// RoleSupport belongs to the one row that is the support desk — the second side of every
	// ticket thread. The desk is resolved by this rather than by its username, because a username
	// is something a user can register and whatever answers "who is support" reads every ticket
	// thread on the platform. No route grants or takes it: a migration puts it on that row, and a
	// partial unique index keeps it to one.
	RoleSupport Role = "support"
)

// usernameRe is the wire contract for a username. Lowercase only, because the
// column has a lowercase CHECK and a plain UNIQUE is what makes it a key lookup.
var usernameRe = regexp.MustCompile(`^[a-z0-9._-]+$`)

// Account is the aggregate root: the identity row, the profile sharing its key, and the
// federated links — the two things a rule spans together with the row. A saved address
// spans nothing up here, so Contact is its own aggregate.
//
// Fields are exported and callers mutate them directly; Validate decides whether the
// result is legal and Save runs it before writing. The methods below are for what an
// assignment cannot do: a rule only the root sees, a change with a consequence, a fact
// worth recording.
type Account struct {
	ID int64
	// Version is the optimistic lock: Save writes `WHERE version = @version`, so a command
	// built on a stale read is refused rather than overwriting what happened in between.
	Version int64
	// Each identifier is optional alone and at least one has to be set. nil reaches the
	// database as NULL, so the UNIQUE constraints keep working.
	Status   Status  `validate:"required,oneof=active suspended"`
	Role     Role    `validate:"required,oneof=user moderator admin support"`
	Phone    *string `validate:"omitempty,e164"`
	Email    *string `validate:"omitempty,email,max=255"`
	Username *string `validate:"omitempty,min=3,max=100"`
	// PasswordHash is nil on a provider-only account, which is what makes unlinking the
	// last provider refusable.
	PasswordHash     *string
	EmailVerified    bool
	CreatedAt        time.Time
	SuspendedUntil   *time.Time
	SuspensionReason *string

	// Children. Pointers so Save fills an id in place; `-` because each owns its rules.
	Profile    Profile          `validate:"-"`
	Identities []*OAuthIdentity `validate:"-"`

	events []Event
}

// NewAccount builds the aggregate from already-hashed credentials, normalizing the
// identifiers here so every path into the table stores them the same way.
func NewAccount(role Role, email, phone, username, passwordHash string, profile Profile) (*Account, error) {
	a := &Account{Version: 1, Status: StatusActive, Role: role, Profile: profile}
	// An identifier that arrives empty is not set at all: nil is the one way to spell
	// absent, so the UNIQUE columns keep working and no reader has to test for "".
	if s := NormalizeIdentifier(email); s != "" {
		a.Email = &s
	}
	if s := strings.TrimSpace(phone); s != "" {
		a.Phone = &s
	}
	if s := NormalizeIdentifier(username); s != "" {
		a.Username = &s
	}
	if passwordHash != "" {
		a.PasswordHash = &passwordHash
	}
	if err := a.Validate(); err != nil {
		return nil, err
	}
	return a, nil
}

// NewOAuthAccount creates an account that signs in through a provider. The link is part of
// the same construction because without it there is no way in, so the aggregate would not
// validate — the account and its first link are one fact.
func NewOAuthAccount(email, username string, profile Profile, provider, uid string) (*Account, error) {
	link, err := NewOAuthIdentity(0, provider, uid)
	if err != nil {
		return nil, err
	}
	a := &Account{
		Version:    1,
		Status:     StatusActive,
		Role:       RoleUser,
		Profile:    profile,
		Identities: []*OAuthIdentity{link},
	}
	if s := NormalizeIdentifier(email); s != "" {
		a.Email = &s
	}
	if s := NormalizeIdentifier(username); s != "" {
		a.Username = &s
	}
	record(a, IdentityLinked, ProviderLink{Provider: provider})
	if err := a.Validate(); err != nil {
		return nil, err
	}
	return a, nil
}

// SupportUsername is the username the support desk's row carries — reserved, so nobody else can
// wear the platform's own name in a thread. It is not what resolves the desk: RoleSupport is.
const SupportUsername = "support"

// Validate checks the whole aggregate — what makes exported children safe: a caller that
// breaks an invariant by hand is refused at the write rather than stored.
func (a *Account) Validate() error {
	if err := validation.Default().Struct(a); err != nil {
		return validation.AsError(err)
	}
	if a.Username != nil && !usernameRe.MatchString(*a.Username) {
		return errx.NewValidationError("invalid field: username", errx.Field{
			Field:   "username",
			Rule:    "pattern",
			Message: "must contain only lowercase letters, digits, dot, dash or underscore",
		})
	}
	// The desk's username is the platform's own, and only the desk may wear it. Checked on every
	// write rather than on an insert: a guard that fires only for a new row is walked past by a
	// rename.
	if a.Role != RoleSupport && a.Username != nil && *a.Username == SupportUsername {
		return ErrUsernameReserved
	}
	if !a.HasIdentifier() {
		return ErrNoIdentifier
	}
	// The desk is not an account anybody signs in as — no password and no provider is the point of
	// it — so "at least one way in" is a rule about the accounts that have a person behind them.
	if a.Role != RoleSupport && a.SignInMethods() == 0 {
		return ErrLastSignInMethod
	}
	if err := a.Profile.Validate(); err != nil {
		return err
	}
	seen := make(map[string]bool, len(a.Identities))
	for _, i := range a.Identities {
		if err := ValidateProvider(i.Provider); err != nil {
			return err
		}
		if seen[i.Provider] {
			return ErrIdentifierTaken
		}
		seen[i.Provider] = true
	}
	return nil
}

func (a *Account) HasIdentifier() bool {
	return a.Email != nil || a.Phone != nil || a.Username != nil
}

func (a *Account) HasPassword() bool { return a.PasswordHash != nil }

// SignInMethods counts every way into the account: the password on the row plus every
// linked provider.
func (a *Account) SignInMethods() int {
	n := len(a.Identities)
	if a.HasPassword() {
		n++
	}
	return n
}

// IsSuspended reports whether the suspension is still in force. A suspension with
// no deadline is permanent; one whose deadline has passed is over, because nothing
// else in the system rewrites the row when the clock runs out.
func (a *Account) IsSuspended(now time.Time) bool {
	if a.Status != StatusSuspended {
		return false
	}
	return a.SuspendedUntil == nil || a.SuspendedUntil.After(now)
}

// --- changes with a consequence ---

// SetEmail takes the address itself; an empty one means the same as ClearEmail, so there
// stays exactly one way to spell absent. A new address is unverified by definition.
func (a *Account) SetEmail(email string) {
	next := NormalizeIdentifier(email)
	if next == "" {
		a.ClearEmail()
		return
	}
	if a.Email != nil && *a.Email == next {
		return
	}
	record(a, EmailChanged, IdentifierChange{From: a.Email, To: &next})
	a.Email, a.EmailVerified = &next, false
}

// ClearEmail removes the address, which Validate refuses when it is the last identifier.
func (a *Account) ClearEmail() {
	if a.Email == nil {
		return
	}
	record(a, EmailChanged, IdentifierChange{From: a.Email})
	a.Email, a.EmailVerified = nil, false
}

func (a *Account) SetPhone(phone string) {
	next := strings.TrimSpace(phone)
	if next == "" {
		a.ClearPhone()
		return
	}
	if a.Phone != nil && *a.Phone == next {
		return
	}
	record(a, PhoneChanged, IdentifierChange{From: a.Phone, To: &next})
	a.Phone = &next
}

func (a *Account) ClearPhone() {
	if a.Phone == nil {
		return
	}
	record(a, PhoneChanged, IdentifierChange{From: a.Phone})
	a.Phone = nil
}

func (a *Account) SetUsername(username string) {
	next := NormalizeIdentifier(username)
	if next == "" {
		a.ClearUsername()
		return
	}
	if a.Username != nil && *a.Username == next {
		return
	}
	record(a, UsernameChanged, IdentifierChange{From: a.Username, To: &next})
	a.Username = &next
}

func (a *Account) ClearUsername() {
	if a.Username == nil {
		return
	}
	record(a, UsernameChanged, IdentifierChange{From: a.Username})
	a.Username = nil
}

// SetPassword takes an already-hashed secret. The plain one never reaches the domain, and
// an empty hash means no password — the state a provider-only account is in.
func (a *Account) SetPassword(hash string) {
	record(a, PasswordChanged, NoPayload{})
	a.PasswordHash = nil
	if hash != "" {
		a.PasswordHash = &hash
	}
}

// MarkEmailVerified is idempotent: verifying twice is not an event.
func (a *Account) MarkEmailVerified() {
	if a.EmailVerified {
		return
	}
	record(a, EmailVerified, EmailVerification{Email: a.Email})
	a.EmailVerified = true
}

// Suspend records the outcome of an upheld report. A nil deadline is permanent.
func (a *Account) Suspend(reason string, until *time.Time) {
	a.Status = StatusSuspended
	a.SuspendedUntil = until
	a.SuspensionReason = nil
	if reason != "" {
		a.SuspensionReason = &reason
	}
	record(a, Suspended, Suspension{Status: a.Status, Reason: reason, Until: until})
}

// Reinstate lifts a suspension and clears its details with it — the row carries
// only the suspension in force, and past ones live in the audit log.
func (a *Account) Reinstate() {
	a.Status = StatusActive
	a.SuspensionReason = nil
	a.SuspendedUntil = nil
	record(a, Reinstated, StatusChange{Status: a.Status})
}

// SetRole grants or takes away staff powers. Named for the code it records, because the
// audit trail is the point of the change being a method.
func (a *Account) SetRole(role Role) {
	if a.Role == role {
		return
	}
	event := RoleRevoked
	if role == RoleModerator || role == RoleAdmin {
		event = RoleGranted
	}
	a.Role = role
	record(a, event, RoleChange{Role: role})
}

// --- federated identities ---

// Link records a federated identity. One identity per provider, which the unique index
// also says — this says it before the round trip.
func (a *Account) Link(provider, uid string) (*OAuthIdentity, error) {
	next, err := NewOAuthIdentity(a.ID, provider, uid)
	if err != nil {
		return nil, err
	}
	if slices.ContainsFunc(a.Identities, func(i *OAuthIdentity) bool { return i.Provider == next.Provider }) {
		return nil, ErrIdentifierTaken
	}
	a.Identities = append(a.Identities, next)
	record(a, IdentityLinked, ProviderLink{Provider: provider})
	return next, nil
}

// Unlink removes a federated identity, refusing when it is the last way in — only the
// root sees both halves of that rule. Nothing to record beyond the event: the slice is the
// whole set and Save deletes whatever left it.
func (a *Account) Unlink(provider string) error {
	at := slices.IndexFunc(a.Identities, func(i *OAuthIdentity) bool { return i.Provider == provider })
	if at < 0 {
		return ErrOAuthIdentityNotFound
	}
	if a.SignInMethods() <= 1 {
		return ErrLastSignInMethod
	}
	a.Identities = slices.Delete(a.Identities, at, at+1)
	record(a, IdentityUnlinked, ProviderLink{Provider: provider})
	return nil
}

// AccountSnapshot is the row as the audit log keeps it — identifiers included, password
// never. Its JSON is `audit_log.snapshot`.
type AccountSnapshot struct {
	ID               int64      `json:"id"`
	Version          int64      `json:"version"`
	Status           Status     `json:"status"`
	Role             Role       `json:"role"`
	Email            *string    `json:"email"`
	Phone            *string    `json:"phone"`
	Username         *string    `json:"username"`
	EmailVerified    bool       `json:"email_verified"`
	SuspendedUntil   *time.Time `json:"suspended_until"`
	SuspensionReason *string    `json:"suspension_reason"`
	CreatedAt        time.Time  `json:"created_at"`
}

func (a *Account) Snapshot() AccountSnapshot {
	return AccountSnapshot{
		ID:               a.ID,
		Version:          a.Version,
		Status:           a.Status,
		Role:             a.Role,
		Email:            a.Email,
		Phone:            a.Phone,
		Username:         a.Username,
		EmailVerified:    a.EmailVerified,
		SuspendedUntil:   a.SuspendedUntil,
		SuspensionReason: a.SuspensionReason,
		CreatedAt:        a.CreatedAt,
	}
}

// NormalizeIdentifier is the stored form of anything an account is looked up by — an email, a
// username, or the identifier a login sent before anyone knows which of the two it is: lowercase
// and trimmed, which is what makes a plain UNIQUE index enough and a lookup exact. One function
// because one rule; a second name for the same body only tells a reader they differ.
func NormalizeIdentifier(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// GenerateUsername invents one for an account that arrived with no email and no
// username — Apple's "hide my email". The suffix is random rather than derived
// from the provider's subject id, which would leak it.
func GenerateUsername() (string, error) {
	var b [5]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return "u" + hex.EncodeToString(b[:]), nil
}
