package catalog

import (
	"context"

	catalogapi "shopnexus/internal/module/catalog/api"
)

// RecordInteractions publishes a batch of shopper actions and returns as soon as they are
// queued. Best-effort by design, the same bargain telemetry makes: a bus that is down costs a
// slower feed and a slightly stale popularity score, never a failed request over something the
// caller cannot fix.
func (s *Service) RecordInteractions(ctx context.Context, req catalogapi.RecordInteractionsRequest) error {
	for _, in := range req.Interactions {
		event := ListingInteraction{
			AccountID: req.ActorID.Int64(),
			ListingID: in.ListingID.Int64(),
			Type:      in.Type,
		}
		if err := publishListingInteraction(ctx, s.bus, event); err != nil {
			s.log.Warn("publish listing interaction", "listing_id", in.ListingID, "type", in.Type, "err", err)
		}
	}
	return nil
}
