package order

import (
	"context"
	"fmt"

	orderapi "shopnexus/internal/module/order/api"
	"shopnexus/internal/module/order/domain"
	"shopnexus/internal/shared/validation"
)

// Who was behind each fact. Derived from the code rather than stored: only a seller confirms,
// only a buyer signs for a parcel, only a carrier moves one. An account id in the trail would
// be a second copy of that, free to disagree with it.
const (
	actorBuyer   = "buyer"
	actorSeller  = "seller"
	actorCarrier = "carrier"
	actorSystem  = "system"
)

var actorOf = map[domain.EventCode]string{
	domain.Placed.Code:                actorBuyer,
	domain.Confirmed.Code:             actorSeller,
	domain.Declined.Code:              actorSeller,
	domain.ConfirmationEscalated.Code: actorSystem,
	domain.ShipmentAdvanced.Code:      actorCarrier,
	domain.Received.Code:              actorBuyer,
	domain.Cancelled.Code:             actorBuyer,
	domain.Completed.Code:             actorSystem,
	domain.PayoutReleased.Code:        actorSystem,
}

// historyLimit caps the trail. An order collects one entry per transition plus one per carrier
// checkpoint, so this is a guard against a pathological row rather than a page size — which is
// also why the route takes no paging parameters.
const historyLimit = 100

// ListOrderHistory answers what happened to an order, newest first.
//
// Both parties read the same rows: every fact here is one side acting on a sale the other side
// is party to, so there is nothing in it to keep from either of them. A stranger gets the same
// not-found the order itself gives.
func (s *Service) ListOrderHistory(ctx context.Context, req orderapi.ListOrderHistoryRequest) ([]orderapi.OrderHistoryEntry, error) {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return nil, err
	}
	if _, err := s.involved(ctx, req.ActorID, req.ID); err != nil {
		return nil, err
	}

	records, _, err := s.repo.ListOrderHistory(ctx, req.ID.Int64(), 0, historyLimit)
	if err != nil {
		return nil, fmt.Errorf("list order history: %w", err)
	}

	out := make([]orderapi.OrderHistoryEntry, 0, len(records))
	for _, rec := range records {
		entry := orderapi.OrderHistoryEntry{
			Version:   rec.Version,
			Code:      rec.Code,
			ChangedAt: rec.ChangedAt,
			ActorKind: actorKind(rec.Code),
		}
		// The payload is read by the field each fact declares, not by walking the map: a
		// missing key is an absent value rather than a rendering to guess at.
		entry.Reason = diffString(rec.Diff, "reason")
		entry.ShipmentStatus = diffString(rec.Diff, "status")
		entry.Evidence = diffInt(rec.Diff, "evidence")
		out = append(out, entry)
	}
	return out, nil
}

// actorKind falls back to `system` for a code this build does not know: a trail read by an
// older binary than the one that wrote it should still render.
func actorKind(code string) string {
	if kind, ok := actorOf[domain.EventCode(code)]; ok {
		return kind
	}
	return actorSystem
}

func diffString(diff map[string]any, key string) string {
	if v, ok := diff[key].(string); ok {
		return v
	}
	return ""
}

// diffInt reads a number back out of jsonb, which decodes every number as float64.
func diffInt(diff map[string]any, key string) int {
	switch v := diff[key].(type) {
	case float64:
		return int(v)
	case int64:
		return int(v)
	}
	return 0
}
