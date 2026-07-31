package domain

import "slices"

// EventCode is the name a fact is stored and published under: `audit_log.code`, and the
// bus topic when one is wired.
type EventCode string

// EventType binds a code to the payload recorded with it, the way eventbus.Topic binds a
// name to what travels on it. Declaring the pair once is what makes the recording site and
// the reader agree without either of them naming a map key.
type EventType[T any] struct{ Code EventCode }

func newEventType[T any](code EventCode) EventType[T] { return EventType[T]{Code: code} }

// The facts this module records. `listing.publish` is the code the schema already names as
// its example, so the vocabulary is `listing.*`.
var (
	Published      = newEventType[StatusChange]("listing.publish")
	Approved       = newEventType[StatusChange]("listing.approve")
	TakenDown      = newEventType[Takedown]("listing.takedown")
	Hidden         = newEventType[StatusChange]("listing.hide")
	EditSubmitted  = newEventType[EditSubmission]("listing.edit_submitted")
	VariantAdded   = newEventType[VariantChange]("listing.variant_added")
	VariantRemoved = newEventType[VariantChange]("listing.variant_removed")
	Deleted        = newEventType[NoPayload]("listing.delete")
)

// The payloads. Their JSON is what lands in `audit_log.diff`, so the tags are the trail's
// column names and changing one rewrites how history reads.
type (
	StatusChange struct {
		Status Status `json:"status"`
	}

	Takedown struct {
		Status Status `json:"status"`
		Reason string `json:"reason"`
	}

	// EditSubmission records which fields the seller asked to change, not their values:
	// the values are in `pending_edit` on the row, and duplicating them would let the two
	// drift.
	EditSubmission struct {
		Fields []string `json:"fields"`
	}

	VariantChange struct {
		VariantID int64 `json:"variant_id"`
	}

	// NoPayload is a fact with nothing to say beyond having happened.
	NoPayload struct{}
)

// Event is a fact the aggregate decided. It is not an instruction: Save persists the
// struct's state, never this list, so deleting every record call still leaves the database
// right — it only loses the trail.
type Event struct {
	Code    EventCode
	Payload any
}

// record appends a fact. A free function rather than a method because Go has no generic
// methods, and the payload's type has to come from the event type.
func record[T any](l *Listing, e EventType[T], payload T) {
	l.events = append(l.events, Event{Code: e.Code, Payload: payload})
}

func (l *Listing) Events() []Event { return slices.Clone(l.events) }

// PayloadOf reads a payload back at its declared type. False when the event is a different
// fact, so a caller cannot quietly read the wrong shape out of an `any`.
func PayloadOf[T any](e Event, t EventType[T]) (T, bool) {
	if e.Code != t.Code {
		var zero T
		return zero, false
	}
	p, ok := e.Payload.(T)
	return p, ok
}

// Happened answers "did this command actually do the thing". Ask it before Save, which
// clears the events once they are audited.
func (l *Listing) Happened(code EventCode) bool {
	return slices.ContainsFunc(l.events, func(e Event) bool { return e.Code == code })
}

// ClearEvents is called by the repository once the trail is committed, so a second Save of
// the same aggregate does not record the same facts twice.
func (l *Listing) ClearEvents() { l.events = nil }
