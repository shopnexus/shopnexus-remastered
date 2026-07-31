package domain

import (
	"slices"
	"time"
)

// EventCode is the name a fact is stored and published under: `audit_log.code`, and the
// bus topic when one is wired.
type EventCode string

// EventType binds a code to the payload recorded with it, the way eventbus.Topic binds a
// name to what travels on it. Declaring the pair once is what makes the recording site and
// the reader agree without either of them naming a map key.
type EventType[T any] struct{ Code EventCode }

func newEventType[T any](code EventCode) EventType[T] { return EventType[T]{Code: code} }

// The facts this module records. One declaration per fact, so "what shape does this
// carry" is answered where the code is named and a consumer is one grep away.
var (
	EmailChanged     = newEventType[IdentifierChange]("account.email_changed")
	PhoneChanged     = newEventType[IdentifierChange]("account.phone_changed")
	UsernameChanged  = newEventType[IdentifierChange]("account.username_changed")
	PasswordChanged  = newEventType[NoPayload]("account.password_changed")
	EmailVerified    = newEventType[EmailVerification]("account.email_verified")
	Suspended        = newEventType[Suspension]("account.suspend")
	Reinstated       = newEventType[StatusChange]("account.reinstate")
	RoleGranted      = newEventType[RoleChange]("account.grant_moderator")
	RoleRevoked      = newEventType[RoleChange]("account.revoke_moderator")
	IdentityLinked   = newEventType[ProviderLink]("account.identity_linked")
	IdentityUnlinked = newEventType[ProviderLink]("account.identity_unlinked")
	// IdentityVerdict is about identity_document rather than the account aggregate, so it
	// is written through InsertAuditLog instead of by Save.
	IdentityVerdict = newEventType[Verdict]("identity_document.verdict")
)

// The payloads. Their JSON is what lands in `audit_log.diff`, so the tags are the trail's
// column names and changing one rewrites how history reads.
type (
	// IdentifierChange carries both ends, nil for "not set", so the trail shows a removal
	// as well as a replacement.
	IdentifierChange struct {
		From *string `json:"from"`
		To   *string `json:"to"`
	}

	EmailVerification struct {
		Email *string `json:"email"`
	}

	Suspension struct {
		Status Status     `json:"status"`
		Reason string     `json:"suspension_reason"`
		Until  *time.Time `json:"suspended_until"`
	}

	StatusChange struct {
		Status Status `json:"status"`
	}

	RoleChange struct {
		Role Role `json:"role"`
	}

	ProviderLink struct {
		Provider string `json:"provider"`
	}

	Verdict struct {
		Status          IdentityStatus `json:"status"`
		RejectionReason *string        `json:"rejection_reason"`
		ExpiresAt       *time.Time     `json:"expires_at"`
	}

	// NoPayload is a fact with nothing to say beyond having happened. It marshals to {},
	// which is what the NOT NULL diff column wants.
	NoPayload struct{}
)

// Event is a fact the aggregate decided. It is not an instruction: Save persists the
// struct's state, never this list, so deleting every record call still leaves the database
// right — it only loses the trail.
//
// Payload is `any` because one slice holds facts of different shapes; both ends of it are
// typed, by record on the way in and PayloadOf on the way out.
type Event struct {
	Code    EventCode
	Payload any
}

// record appends a fact. A free function rather than a method because Go has no generic
// methods, and the payload's type has to come from the event type.
func record[T any](a *Account, e EventType[T], payload T) {
	a.events = append(a.events, Event{Code: e.Code, Payload: payload})
}

// Events is what this command decided, for the audit rows Save writes and for whatever
// the service publishes afterwards.
func (a *Account) Events() []Event { return slices.Clone(a.events) }

// PayloadOf reads a payload back at its declared type. False when the event is a different
// fact, so a caller cannot quietly read the wrong shape out of it.
func PayloadOf[T any](e Event, t EventType[T]) (T, bool) {
	if e.Code != t.Code {
		var zero T
		return zero, false
	}
	p, ok := e.Payload.(T)
	return p, ok
}

// Happened answers "did this command actually do the thing", so a caller stops
// snapshotting a field before the change to compare it after.
func (a *Account) Happened(code EventCode) bool {
	return slices.ContainsFunc(a.events, func(e Event) bool { return e.Code == code })
}

// ClearEvents is called by the repository once the trail is committed, so a second Save
// of the same aggregate does not record the same facts twice.
func (a *Account) ClearEvents() { a.events = nil }
