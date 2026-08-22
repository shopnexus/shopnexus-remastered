package catalog

import (
	"context"

	"shopnexus/internal/infra/eventbus"
)

// ListingInteraction is a shopper's action against one listing — a fact, not an instruction:
// nothing downstream is required to act on it. AccountID is 0 for an anonymous view, which is
// still a real signal for the platform's popularity score, though not for personalisation.
type ListingInteraction struct {
	AccountID int64  `json:"account_id"`
	ListingID int64  `json:"listing_id"`
	Type      string `json:"type"`
}

// ListingInteractionTopic carries ListingInteraction. Declared once here, so nothing else
// names the string. Mirrored as a literal in observability/events.go, which subscribes
// without importing this package: change it here and there together.
var ListingInteractionTopic = eventbus.NewTopic[ListingInteraction]("catalog.listing_interaction")

func publishListingInteraction(ctx context.Context, bus eventbus.Client, event ListingInteraction) error {
	return eventbus.Publish(ctx, bus, ListingInteractionTopic, event)
}

// ListingModerated is a moderator's decision on somebody's listing, published so the seller can
// be told. Until now the decision only reached `audit_log`: `Takedown.NotifySeller` was recorded
// and acted on by nothing, because this module has no seam to the one that owns notifications
// and reaching for one would be a dependency cycle fx cannot construct.
//
// Published for a takedown *whether or not* the moderator asked to notify — the flag is their
// choice and the subscriber's to honour, and burying it here would put a product rule in a
// publisher. An approval always goes out: a seller waiting on a queue is the one person who
// asked to be told.
type ListingModerated struct {
	ListingID int64 `json:"listing_id"`
	SellerID  int64 `json:"seller_id"`
	// Name travels with it so a subscriber does not have to read back into this module for the
	// one string it needs to write a sentence.
	Name     string `json:"name"`
	Approved bool   `json:"approved"`
	// Reason is the moderator's note on a takedown, empty on an approval.
	Reason string `json:"reason"`
	// NotifySeller is the moderator's choice, carried rather than applied: see above.
	NotifySeller bool `json:"notify_seller"`
}

// ListingModeratedTopic carries ListingModerated.
var ListingModeratedTopic = eventbus.NewTopic[ListingModerated]("catalog.listing_moderated")

func publishListingModerated(ctx context.Context, bus eventbus.Client, event ListingModerated) error {
	return eventbus.Publish(ctx, bus, ListingModeratedTopic, event)
}
