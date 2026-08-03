package account

import (
	"context"
	"fmt"
	"log/slog"

	"shopnexus/internal/infra/eventbus"
	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/module/order"
	"shopnexus/internal/shared/id"
)

// SubscribeOrderEvents turns order facts into feed rows.
//
// One notification per interested party, because a notification belongs to an
// account: an order fact has two of them and there is no shared inbox. The handler
// is not idempotent and does not need to be — a redelivered order event costs a
// duplicate feed row, which is a cosmetic fault, and de-duplicating it would need a
// uniqueness rule the feed does not have.
func SubscribeOrderEvents(bus eventbus.Client, svc accountapi.Service, log *slog.Logger) {
	eventbus.Subscribe(bus, order.OrderPlacedTopic, "account", func(ctx context.Context, e order.OrderPlaced) error {
		return notifyBoth(ctx, svc, log, e.BuyerID, e.SellerID, "Order placed", map[string]any{
			"order_id": id.Of[id.Order](e.OrderID),
		})
	})

	eventbus.Subscribe(bus, order.OrderSettledTopic, "account", func(ctx context.Context, e order.OrderSettled) error {
		return notifyBoth(ctx, svc, log, e.BuyerID, e.SellerID, "Order completed", map[string]any{
			"order_id": id.Of[id.Order](e.OrderID),
		})
	})
}

// notifyBoth writes to both sides and reports the first failure, so the bus retries
// the pair rather than silently delivering half of it.
func notifyBoth(ctx context.Context, svc accountapi.Service, log *slog.Logger, buyerID, sellerID int64, title string, payload map[string]any) error {
	for _, accountID := range [...]int64{buyerID, sellerID} {
		if accountID == 0 {
			continue
		}
		_, err := svc.CreateNotification(ctx, accountapi.CreateNotificationRequest{
			AccountID: id.Of[id.Account](accountID),
			Category:  string(domain.CategoryOrder),
			Title:     title,
			Payload:   payload,
		})
		if err != nil {
			log.Error("create order notification failed", "account_id", accountID, "title", title, "err", err)
			return fmt.Errorf("notify account %d: %w", accountID, err)
		}
	}
	return nil
}
