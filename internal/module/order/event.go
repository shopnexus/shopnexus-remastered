package order

import (
	"context"

	"shopnexus/internal/infra/eventbus"
	financedomain "shopnexus/internal/module/finance/domain"
	orderapi "shopnexus/internal/module/order/api"
	"shopnexus/internal/shared/realtime"
)

// OrderPlaced is published after an order is written and its escrow held. A fact, not an
// instruction: nothing downstream is required to act on it, and the order is already real
// whether or not anybody does.
//
// Lines carries what was sold, not just that a sale happened: catalog's own subscriber folds
// each one into the buyer's listing_signal (a purchase is the strongest positive signal this
// platform has), and observability's folds it into the listing's popularity score. Neither
// would have anywhere to start without it — a purchase this event named only by OrderID would
// send both subscribers back to this module's own tables to find out what was bought, which is
// exactly the kind of coupling a published fact exists to avoid.
type OrderPlaced struct {
	// Raw keys: this never leaves the process as a public payload, and a consumer wants the
	// database key anyway.
	OrderID  int64       `json:"order_id"`
	BuyerID  int64       `json:"buyer_id"`
	SellerID int64       `json:"seller_id"`
	Total    int64       `json:"total"`
	Currency string      `json:"currency"`
	Lines    []OrderLine `json:"lines"`
}

// OrderLine is one purchased line, as far as a subscriber outside this module ever needs it.
type OrderLine struct {
	ListingID int64 `json:"listing_id"`
	VariantID int64 `json:"variant_id"`
	Quantity  int64 `json:"quantity"`
}

// OrderPlacedTopic carries OrderPlaced. Declared once here, so nothing else names the string.
// The name is also mirrored as a literal in observability/events.go, which subscribes without
// importing this package: change it here and there together.
var OrderPlacedTopic = eventbus.NewTopic[OrderPlaced]("order.placed")

func publishOrderPlaced(ctx context.Context, bus eventbus.Client, event OrderPlaced) error {
	return eventbus.Publish(ctx, bus, OrderPlacedTopic, event)
}

// OrderDelivered is the outbound parcel arriving, as the carrier or a moderator reported it.
// The return leg does not publish this: it carries goods the other way and closes a refund.
type OrderDelivered struct {
	OrderID  int64 `json:"order_id"`
	BuyerID  int64 `json:"buyer_id"`
	SellerID int64 `json:"seller_id"`
}

// OrderDeliveredTopic carries OrderDelivered. Declared once here, so nothing else names the
// string.
var OrderDeliveredTopic = eventbus.NewTopic[OrderDelivered]("order.delivered")

func publishOrderDelivered(ctx context.Context, bus eventbus.Client, event OrderDelivered) error {
	return eventbus.Publish(ctx, bus, OrderDeliveredTopic, event)
}

// OrderSettled is published when an order reaches an outcome — paid out or cancelled. A
// fact, like OrderPlaced: the row already says so, and a consumer that misses it is behind
// rather than wrong.
type OrderSettled struct {
	OrderID  int64 `json:"order_id"`
	BuyerID  int64 `json:"buyer_id"`
	SellerID int64 `json:"seller_id"`
	// Completed tells the two outcomes apart. A boolean rather than the state string,
	// because there are exactly two and a third would need a new consumer anyway.
	Completed bool `json:"completed"`
}

// OrderSettledTopic carries OrderSettled. Declared once here, so nothing else names the
// string.
var OrderSettledTopic = eventbus.NewTopic[OrderSettled]("order.settled")

func publishOrderSettled(ctx context.Context, bus eventbus.Client, event OrderSettled) error {
	return eventbus.Publish(ctx, bus, OrderSettledTopic, event)
}

// RefundResolved is the staff verdict on a refund. Trust opened the ticket that asked for it and
// closes it on this, which is why the Note travels here: there is no column for a moderator's
// reasoning on the refund row, and the ticket is where that decision is read.
type RefundResolved struct {
	RefundID int64 `json:"refund_id"`
	// OrderID is what the verdict was about: the escrow, which lives on the order. A refund id is
	// only resolvable inside this module, so a subscriber that carries the fact anywhere else has
	// nothing to name the sale by.
	OrderID int64 `json:"order_id"`
	// BuyerID and SellerID are both parties, because both are told the verdict — a decision on
	// somebody's money that only one side hears about is one the other finds out from their
	// balance. They were left out while nobody read them; account's notifier does now.
	BuyerID  int64 `json:"buyer_id"`
	SellerID int64 `json:"seller_id"`
	// ModeratorID is who decided. The ticket trust closes on this records an author, and a verdict
	// nobody signed is one nobody can be asked about.
	ModeratorID int64 `json:"moderator_id"`
	// BuyerWins is the verdict. What it did to the money depends on whether the goods had
	// already come back, which is the refund's own status to report.
	BuyerWins bool   `json:"buyer_wins"`
	Note      string `json:"note,omitempty"`
}

// RefundResolvedTopic carries RefundResolved. Declared once here, so nothing else names the
// string.
var RefundResolvedTopic = eventbus.NewTopic[RefundResolved]("order.refund_resolved")

func publishRefundResolved(ctx context.Context, bus eventbus.Client, event RefundResolved) error {
	return eventbus.Publish(ctx, bus, RefundResolvedTopic, event)
}

// Escalation causes: what put a refund with staff, for the two paths where nobody wrote a ticket.
// A moderator's first move differs between them — chase a seller who never answered, or check a
// return only the buyer says arrived — so the queue has to be able to tell them apart.
const (
	EscalationUnanswered    = "seller-unanswered"
	EscalationReturnClaimed = "return-claimed-by-buyer"
)

// RefundEscalated is a refund that reached staff without either party raising a ticket. Trust opens
// it for them, on the buyer's behalf, so the case reaches a human without the buyer having to know
// they were supposed to chase it.
//
// Published rather than called: trust already consumes this module's api, so reaching back the
// other way would be a dependency cycle fx cannot construct. The refund is already `disputed`
// when this goes out — a bus that is down must leave a case waiting on staff, never one still
// waiting on a seller whose window has closed.
type RefundEscalated struct {
	RefundID int64 `json:"refund_id"`
	OrderID  int64 `json:"order_id"`
	// BuyerID is who the ticket is raised for. Unlike RefundResolved, this payload does carry a
	// party, because the subscriber has to name a requester and cannot ask this module for one.
	BuyerID int64 `json:"buyer_id"`
	// Cause is which of the two situations this is. The fact, not the wording: how a ticket reads
	// is trust's, and this module has no business writing a subject line.
	Cause string `json:"cause"`
}

// RefundEscalatedTopic carries RefundEscalated.
var RefundEscalatedTopic = eventbus.NewTopic[RefundEscalated]("order.refund_escalated")

func publishRefundEscalated(ctx context.Context, bus eventbus.Client, event RefundEscalated) error {
	return eventbus.Publish(ctx, bus, RefundEscalatedTopic, event)
}

// OrderConfirmationLapsed is a paid order whose seller never accepted it. The buyer's money is
// held and nothing shipped, so staff pick it up: this platform does not cancel a sale on the
// seller's behalf, and it does not post goods on their behalf either.
type OrderConfirmationLapsed struct {
	OrderID int64 `json:"order_id"`
	// BuyerID is the requester of the ticket: they paid and are waiting.
	BuyerID  int64 `json:"buyer_id"`
	SellerID int64 `json:"seller_id"`
}

// OrderConfirmationLapsedTopic carries OrderConfirmationLapsed.
var OrderConfirmationLapsedTopic = eventbus.NewTopic[OrderConfirmationLapsed]("order.confirmation_lapsed")

func publishOrderConfirmationLapsed(ctx context.Context, bus eventbus.Client, event OrderConfirmationLapsed) error {
	return eventbus.Publish(ctx, bus, OrderConfirmationLapsedTopic, event)
}

// OfferUpdated is every change to a negotiation's standing terms: a counter, an
// acceptance, a withdrawal, an expiry.
//
// One event for all of them rather than one per transition, because a client renders the
// offer's current state and does not branch on how it got there — and the two sides
// alternate, so either party may be the one who caused it.
//
// The code is also the AsyncAPI message name in api/asyncapi.gen.yaml, and
// internal/gateway/asyncapi_contract_test.go fails if the two lists disagree.
var OfferUpdated = realtime.NewEvent[orderapi.Offer]("order.offer_updated")

// financeMovementPosted is the error finance answers when an idempotency key has already
// been used. Named here because the lifecycle treats it as success: a movement that was
// already posted is the state the caller wanted.
var financeMovementPosted = financedomain.ErrMovementAlreadyPosted

// financePaid is the session status that means the money is in. Named here because cancelling
// a line turns on it: from that point the sale is undone by a refund, not by a cancellation.
const financePaid = financedomain.StatusSuccess

// OfferChanged is a negotiation's standing terms moving, told to the side that did not move
// them. The realtime OfferUpdated above pushes the row to a client that already has the thread
// open; this one is for the party who does not — it is what turns a counter into something they
// find in their feed tomorrow morning.
//
// Actor is who caused it, because either side may have: a subscriber has to tell the *other*
// one, and asking this module which side that is would be a call back the way the dependency
// already runs.
type OfferChanged struct {
	OfferID  int64 `json:"offer_id"`
	BuyerID  int64 `json:"buyer_id"`
	SellerID int64 `json:"seller_id"`
	ActorID  int64 `json:"actor_id"`
	// Change is which transition this was: 'countered', 'accepted' or 'withdrawn'. The fact,
	// not the wording — how it reads is the reader's module's business.
	Change string `json:"change"`
	// ListingName travels with it: a subscriber that had to resolve the listing would need
	// catalog, and this module already holds the name it just answered the actor with.
	ListingName string `json:"listing_name"`
	Total       int64  `json:"total"`
	Currency    string `json:"currency"`
}

// The transitions OfferChanged reports. An expiry is not among them: nobody did it, and a feed
// row saying a price nobody took has run out is a notification about the absence of an event.
const (
	OfferChangeCountered = "countered"
	OfferChangeAccepted  = "accepted"
	OfferChangeWithdrawn = "withdrawn"
)

// OfferChangedTopic carries OfferChanged.
var OfferChangedTopic = eventbus.NewTopic[OfferChanged]("order.offer_changed")

func publishOfferChanged(ctx context.Context, bus eventbus.Client, event OfferChanged) error {
	return eventbus.Publish(ctx, bus, OfferChangedTopic, event)
}
