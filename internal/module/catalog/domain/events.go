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
	// Created is the listing's first row in the trail, written by hand on the insert: an
	// insert has no version to guard and no events yet, so nothing else would record it —
	// and a history whose oldest entry is the first publication cannot say when the seller
	// wrote the listing.
	Created       = newEventType[StatusChange]("listing.create")
	Published     = newEventType[StatusChange]("listing.publish")
	Approved      = newEventType[Decision]("listing.approve")
	TakenDown     = newEventType[Takedown]("listing.takedown")
	Hidden        = newEventType[StatusChange]("listing.hide")
	EditSubmitted = newEventType[EditSubmission]("listing.edit_submitted")
	// Edited is the same change written straight through, which is what happens to a
	// listing no buyer is looking at. It carries the same payload as EditSubmitted on
	// purpose: the difference between the two codes is whether a moderator stood in
	// between, not what the seller changed.
	Edited         = newEventType[EditSubmission]("listing.edit")
	VariantAdded   = newEventType[VariantChange]("listing.variant_added")
	VariantRemoved = newEventType[VariantChange]("listing.variant_removed")
	VariantEdited  = newEventType[VariantEdit]("listing.variant_edited")
	Deleted        = newEventType[NoPayload]("listing.delete")
)

// EventCodes is every fact this module can record. Declared as a list because the trail is
// published — `GET /listings/{id}/history` answers these strings and the spec enumerates
// them — so a code added above and nowhere else has to fail a test rather than reach a
// client the generated types told there were only ten.
var EventCodes = []EventCode{
	Created.Code, Published.Code, Approved.Code, TakenDown.Code, Hidden.Code,
	EditSubmitted.Code, Edited.Code, VariantAdded.Code, VariantEdited.Code,
	VariantRemoved.Code, Deleted.Code,
}

// The payloads. Their JSON is what lands in `audit_log.diff`, so the tags are the trail's
// column names and changing one rewrites how history reads.
type (
	StatusChange struct {
		Status Status `json:"status"`
	}

	Takedown struct {
		Status Status `json:"status"`
		Reason string `json:"reason"`
		// NotifySeller is the moderator's choice, recorded rather than acted on: the account
		// module owns notifications and this module has no seam to it yet.
		NotifySeller bool `json:"notify_seller"`
	}

	// Decision is an approval: the status it reached and whatever the moderator wrote down.
	Decision struct {
		Status Status `json:"status"`
		Note   string `json:"note,omitempty"`
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

	// VariantEdit names which of a variant's fields moved, not their values — a price is
	// on the row and in the snapshot, and a third copy is one that can drift.
	VariantEdit struct {
		VariantID int64    `json:"variant_id"`
		Fields    []string `json:"fields"`
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
