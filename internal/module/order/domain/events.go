package domain

import "slices"

// EventCode is the name a fact is stored under: `audit_log.code`.
type EventCode string

// EventType binds a code to the payload recorded with it. Declaring the pair once is what
// makes the recording site and the reader agree without either naming a map key.
type EventType[T any] struct{ Code EventCode }

func newEventType[T any](code EventCode) EventType[T] { return EventType[T]{Code: code} }

// The facts this module records. An order's outcome timestamps say where it *is*; these say
// how it got there, which is the question the order screen could not answer at all — the
// schema has carried an `audit_log` for this module since `common` created it, and nothing
// had ever written a row.
var (
	// Placed is written by hand on the insert: an order is created by the payment webhook,
	// so no transition decides it and nothing else would record it.
	Placed = newEventType[NoPayload]("order.placed")
	// Confirmed is the seller accepting, the only thing that lets the parcel be booked.
	Confirmed = newEventType[NoPayload]("order.confirmed")
	Declined  = newEventType[Refusal]("order.declined")
	// ConfirmationEscalated is staff being asked to chase a silent seller. Not a transition
	// — the order stays where it is — but it is the reason a buyer sees no movement.
	ConfirmationEscalated = newEventType[NoPayload]("order.confirmation_escalated")
	// ShipmentAdvanced is the carrier's own report, translated into this platform's
	// vocabulary. The status column only holds the latest, so without this the parcel's
	// journey was overwritten at every step.
	ShipmentAdvanced = newEventType[ShipmentMove]("order.shipment_advanced")
	Received         = newEventType[Receipt]("order.received")
	Cancelled        = newEventType[NoPayload]("order.cancelled")
	// Completed and PayoutReleased are recorded by the writes that decide them: the escrow
	// claim and the release are repo-guarded conditional statements with no transition on this
	// aggregate to hang them off. Everything above is drained from the aggregate by SaveOrder —
	// one mechanism per fact, or a fact lands twice.
	Completed      = newEventType[NoPayload]("order.completed")
	PayoutReleased = newEventType[NoPayload]("order.payout_released")
)

// EventCodes is every fact this module can record. A list because the trail is published —
// `GET /orders/{id}/history` answers these strings — so a code added above and nowhere else
// has to fail a test rather than reach a client whose generated types never mentioned it.
var EventCodes = []EventCode{
	Placed.Code, Confirmed.Code, Declined.Code, ConfirmationEscalated.Code,
	ShipmentAdvanced.Code, Received.Code, Cancelled.Code, Completed.Code,
	PayoutReleased.Code,
}

// The payloads. Their JSON lands in `audit_log.diff`, so the tags are the trail's column
// names and changing one rewrites how history reads.
type (
	// Refusal is the seller's own words. Required by the contract and kept so the buyer can
	// read why a sale they paid for did not happen.
	Refusal struct {
		Reason string `json:"reason"`
	}

	// ShipmentMove is where the parcel got to, in this platform's vocabulary rather than the
	// carrier's.
	ShipmentMove struct {
		Status string `json:"status"`
	}

	// Receipt counts the evidence rather than listing it: the resource ids are on the row,
	// and a second copy is one that can drift.
	Receipt struct {
		Evidence int `json:"evidence"`
	}

	// NoPayload is a fact with nothing to say beyond having happened.
	NoPayload struct{}
)

// Event is a fact the aggregate decided. Never an instruction: the writes persist the
// struct's state, so deleting every record call leaves the database right and loses only the
// trail.
type Event struct {
	Code    EventCode
	Payload any
}

// record appends a fact. A free function rather than a method because Go has no generic
// methods, and the payload's type has to come from the event type.
func record[T any](o *Order, e EventType[T], payload T) {
	o.events = append(o.events, Event{Code: e.Code, Payload: payload})
}

func (o *Order) Events() []Event { return slices.Clone(o.events) }

// PayloadOf reads a payload back at its declared type. False for a different fact, so a
// caller cannot quietly read the wrong shape out of an `any`.
func PayloadOf[T any](e Event, t EventType[T]) (T, bool) {
	if e.Code != t.Code {
		var zero T
		return zero, false
	}
	p, ok := e.Payload.(T)
	return p, ok
}

// ClearEvents is called by the repository once the trail is committed, so a second save of
// the same aggregate does not record the same facts twice.
func (o *Order) ClearEvents() { o.events = nil }
