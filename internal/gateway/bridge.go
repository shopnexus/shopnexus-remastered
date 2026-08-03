package gateway

import (
	"context"
	"log/slog"

	"shopnexus/internal/infra/eventbus"
	"shopnexus/internal/module/order"
	orderapi "shopnexus/internal/module/order/api"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/realtime"
)

// The realtime codes for facts that already travel on the durable bus. Declared here
// rather than in order, because order publishes the Redis topic and knows nothing about
// sockets — this file is the only thing that translates between the two.
var (
	orderPlaced  = realtime.NewEvent[orderapi.OrderRef]("order.placed")
	orderSettled = realtime.NewEvent[orderapi.OrderRef]("order.settled")
)

// BridgeOrderEvents pushes order facts to the sockets of everyone involved.
//
// order.placed and order.settled already exist on the Redis bus, so their producers are
// untouched: this reads them there and re-publishes to the NATS fan-out. The consumer
// group is a single shared "ws-bridge" — whichever replica receives the Redis message
// broadcasts it, and every replica gets it from NATS. Making the group per-replica would
// broadcast the same fact once per replica.
func BridgeOrderEvents(bus eventbus.Client, f realtime.Fanout, log *slog.Logger) {
	eventbus.Subscribe(bus, order.OrderPlacedTopic, "ws-bridge", func(ctx context.Context, e order.OrderPlaced) error {
		push(ctx, f, log, orderPlaced, e.OrderID, e.BuyerID, e.SellerID)
		return nil
	})

	eventbus.Subscribe(bus, order.OrderSettledTopic, "ws-bridge", func(ctx context.Context, e order.OrderSettled) error {
		push(ctx, f, log, orderSettled, e.OrderID, e.BuyerID, e.SellerID)
		return nil
	})
}

// push notifies both sides of a sale and always reports success to the bus.
//
// Returning an error would nack the Redis message and redeliver it, which for a
// best-effort push means re-broadcasting a fact the sockets that were connected have
// already seen. A lost push is repaired when the client reconnects and re-reads.
func push(ctx context.Context, f realtime.Fanout, log *slog.Logger, e realtime.Event[orderapi.OrderRef], orderID, buyerID, sellerID int64) {
	ref := orderapi.OrderRef{ID: id.Of[id.Order](orderID)}
	for _, accountID := range [...]int64{buyerID, sellerID} {
		if accountID == 0 {
			continue
		}
		if err := realtime.Notify(ctx, f, accountID, e, ref); err != nil {
			log.Warn("bridge realtime notify failed", "code", e.Code, "account_id", accountID, "err", err)
		}
	}
}
