// Package domain: the account entities and the rules that hold whatever calls them.
package domain

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
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

// Role separates a user from the staff who moderate them. There is no seller
// role: the same account buys and sells.
type Role string

const (
	RoleUser      Role = "user"
	RoleModerator Role = "moderator"
	RoleAdmin     Role = "admin"
)

// usernameRe is the wire contract for a username. Lowercase only, because the
// column has a lowercase CHECK and a plain UNIQUE is what makes it a key lookup.
var usernameRe = regexp.MustCompile(`^[a-z0-9._-]+$`)

// Account is the identity record. Each identifier is optional on its own and at
// least one has to be set — an account nobody can be addressed by cannot sign in.
// An empty string means "not set" and reaches the database as NULL, so the UNIQUE
// constraints keep working (many NULLs are allowed, two empty strings are not).
type Account struct {
	ID       int64
	Status   Status `validate:"required,oneof=active suspended"`
	Role     Role   `validate:"required,oneof=user moderator admin"`
	Phone    string `validate:"omitempty,e164"`
	Email    string `validate:"omitempty,email,max=255"`
	Username string `validate:"omitempty,min=3,max=100"`
	// PasswordHash is empty on a provider-only account, which is what HasPassword
	// reports and what makes unlinking the last provider refusable.
	PasswordHash     string
	EmailVerified    bool
	CreatedAt        time.Time
	SuspendedUntil   *time.Time
	SuspensionReason string
}

// NewAccount builds an account from already-hashed credentials. The identifiers
// are normalized here rather than at the edge, so every path into the table —
// register, oauth, moderator provisioning — stores them the same way.
func NewAccount(role Role, email, phone, username, passwordHash string) (Account, error) {
	a := Account{
		Status:       StatusActive,
		Role:         role,
		Email:        NormalizeEmail(email),
		Phone:        strings.TrimSpace(phone),
		Username:     NormalizeUsername(username),
		PasswordHash: passwordHash,
	}
	if err := a.Validate(); err != nil {
		return Account{}, err
	}
	if !a.HasIdentifier() {
		return Account{}, ErrNoIdentifier
	}
	return a, nil
}

func (a Account) Validate() error {
	if err := validation.Default().Struct(a); err != nil {
		return validation.AsError(err)
	}
	if a.Username != "" && !usernameRe.MatchString(a.Username) {
		return errx.NewValidationError("invalid field: username", errx.Field{
			Field:   "username",
			Rule:    "pattern",
			Message: "must contain only lowercase letters, digits, dot, dash or underscore",
		})
	}
	return nil
}

func (a Account) HasIdentifier() bool {
	return a.Email != "" || a.Phone != "" || a.Username != ""
}

func (a Account) HasPassword() bool { return a.PasswordHash != "" }

// IsSuspended reports whether the suspension is still in force. A suspension with
// no deadline is permanent; one whose deadline has passed is over, because nothing
// else in the system rewrites the row when the clock runs out.
func (a Account) IsSuspended(now time.Time) bool {
	if a.Status != StatusSuspended {
		return false
	}
	return a.SuspendedUntil == nil || a.SuspendedUntil.After(now)
}

// SetEmail applies a PATCH: nil clears the address, which is refused when it is
// the last identifier. A new address is unverified by definition — keeping the
// flag would let anyone claim a verified address by editing it.
func (a *Account) SetEmail(v *string) error {
	next := ""
	if v != nil {
		next = NormalizeEmail(*v)
	}
	if next == a.Email {
		return nil
	}
	prev := a.Email
	a.Email = next
	a.EmailVerified = false
	if err := a.ensureIdentifier(func() { a.Email = prev }); err != nil {
		return err
	}
	return a.Validate()
}

func (a *Account) SetPhone(v *string) error {
	next := ""
	if v != nil {
		next = strings.TrimSpace(*v)
	}
	if next == a.Phone {
		return nil
	}
	prev := a.Phone
	a.Phone = next
	if err := a.ensureIdentifier(func() { a.Phone = prev }); err != nil {
		return err
	}
	return a.Validate()
}

func (a *Account) SetUsername(v *string) error {
	next := ""
	if v != nil {
		next = NormalizeUsername(*v)
	}
	if next == a.Username {
		return nil
	}
	prev := a.Username
	a.Username = next
	if err := a.ensureIdentifier(func() { a.Username = prev }); err != nil {
		return err
	}
	return a.Validate()
}

// ensureIdentifier rolls the field back before failing, so a rejected patch leaves
// the entity exactly as it was and a caller can report the error and move on.
func (a *Account) ensureIdentifier(rollback func()) error {
	if a.HasIdentifier() {
		return nil
	}
	rollback()
	return ErrLastIdentifier
}

// Suspend records the outcome of an upheld report. A nil deadline is permanent.
func (a *Account) Suspend(reason string, until *time.Time) {
	a.Status = StatusSuspended
	a.SuspensionReason = reason
	a.SuspendedUntil = until
}

// Reinstate lifts a suspension and clears its details with it — the row carries
// only the suspension in force, and past ones live in the audit log.
func (a *Account) Reinstate() {
	a.Status = StatusActive
	a.SuspensionReason = ""
	a.SuspendedUntil = nil
}

// NormalizeEmail and NormalizeUsername are the stored form: lowercase and
// trimmed, which is what makes a plain UNIQUE index enough and a lookup exact.
func NormalizeEmail(s string) string    { return strings.ToLower(strings.TrimSpace(s)) }
func NormalizeUsername(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

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
