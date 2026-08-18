package observability

import (
	"context"
	"encoding/json/v2"
	"log/slog"
	"time"

	"shopnexus/internal/infra/eventbus"
	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/module/observability/domain"
	"shopnexus/internal/module/observability/port"
)

// listingInteractionPayload is the wire shape of catalog.ListingInteraction, read locally
// rather than by importing that module's service package — the same trade-off observedTopics
// already makes for the topic name itself, so this shape has to be kept in step with
// catalog/event.go by hand.
type listingInteractionPayload struct {
	AccountID int64  `json:"account_id"`
	ListingID int64  `json:"listing_id"`
	Type      string `json:"type"`
}

// subscribePopularity is this module's own consumer group on catalog's fact — independent of
// the raw mirror subscribeEvents registers on the same topic, and independent of catalog's own
// subscriber that feeds personalisation: three groups, one fact, none of them waiting on
// either of the others. catalogapi is imported for its published InteractionWeight map only;
// nothing here depends on catalog's service.
func subscribePopularity(b eventbus.Client, repo port.Repository, log *slog.Logger) {
	b.Subscribe("catalog.listing_interaction", "observability-popularity",
		func(ctx context.Context, payloads [][]byte) error {
			events := make([]domain.ListingInteractionEvent, 0, len(payloads))
			for _, raw := range payloads {
				var p listingInteractionPayload
				if err := json.Unmarshal(raw, &p); err != nil {
					log.Error("unmarshal listing interaction", "err", err)
					continue
				}
				weight, ok := catalogapi.InteractionWeight[p.Type]
				if !ok {
					continue
				}
				events = append(events, domain.ListingInteractionEvent{
					ListingID: p.ListingID, Type: p.Type, Weight: weight,
				})
			}
			if err := repo.ApplyPopularityDeltas(ctx, events); err != nil {
				log.Error("apply popularity deltas", "err", err)
				return err
			}
			return nil
		}, eventbus.SubscribeOptions{BatchSize: 50, Linger: 2 * time.Second})
}

// orderPlacedPayload is the wire shape of order.OrderPlaced, read locally for the same reason
// listingInteractionPayload is: this module does not import order's package.
type orderPlacedPayload struct {
	Lines []struct {
		ListingID int64 `json:"listing_id"`
	} `json:"lines"`
}

// subscribePurchases folds a completed sale into the same popularity score a view or a click
// does — a purchase is the strongest signal this platform has, and it is not the
// catalog.listing_interaction fact, so it needs its own subscription to order's.
func subscribePurchases(b eventbus.Client, repo port.Repository, log *slog.Logger) {
	weight := catalogapi.InteractionWeight[catalogapi.InteractionPurchase]
	b.Subscribe("order.placed", "observability-popularity",
		func(ctx context.Context, payloads [][]byte) error {
			var events []domain.ListingInteractionEvent
			for _, raw := range payloads {
				var p orderPlacedPayload
				if err := json.Unmarshal(raw, &p); err != nil {
					log.Error("unmarshal order placed", "err", err)
					continue
				}
				for _, line := range p.Lines {
					events = append(events, domain.ListingInteractionEvent{
						ListingID: line.ListingID, Type: catalogapi.InteractionPurchase, Weight: weight,
					})
				}
			}
			if err := repo.ApplyPopularityDeltas(ctx, events); err != nil {
				log.Error("apply purchase popularity deltas", "err", err)
				return err
			}
			return nil
		}, eventbus.SubscribeOptions{BatchSize: 50, Linger: 2 * time.Second})
}
