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
