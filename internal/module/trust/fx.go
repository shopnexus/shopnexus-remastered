package trust

import (
	"context"
	"fmt"
	"log/slog"

	"go.uber.org/fx"

	"shopnexus/internal/config"
	"shopnexus/internal/infra/eventbus"
	"shopnexus/internal/infra/postgres"
	"shopnexus/internal/module/order"
	trustpg "shopnexus/internal/module/trust/adapter/postgres"
	trustapi "shopnexus/internal/module/trust/api"
	"shopnexus/internal/module/trust/port"
	"shopnexus/internal/shared/id"
)

// Module wires the trust service, its Postgres-backed repository, the blind-window reveal,
// and the subscriber that folds a finished order into both parties' reputation.
var Module = fx.Module("trust",
	fx.Provide(
		fx.Annotate(newRepo, fx.As(new(port.Repository))),
		fx.Annotate(NewService, fx.As(new(trustapi.Service))),
		NewReveal,
	),
	// Eager, because nothing else in the graph depends on a subscription: without this the
	// bus would have no consumer until something happened to ask for the service.
	fx.Invoke(SubscribeSettledOrders),
)

func newRepo(lc fx.Lifecycle, cfg *config.Config) (*trustpg.Repo, error) {
	pool, err := postgres.NewPool(context.Background(), cfg.TrustDBDSN, "trust")
	if err != nil {
		return nil, fmt.Errorf("open trust db pool: %w", err)
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error { pool.Close(); return nil }})
	return trustpg.New(pool), nil
}

// SubscribeSettledOrders keeps the completed and cancelled counters on a reputation —
// "152 completed, 3 cancelled" says something an average cannot.
//
// Order is the authority and this is a mirror, so a redelivered message double-counts and a
// lost one under-counts. Both are repaired by a recount rather than by making the consumer
// clever, which is why these are counters and not decisions.
func SubscribeSettledOrders(bus eventbus.Client, svc trustapi.Service, log *slog.Logger) {
	eventbus.Subscribe(bus, order.OrderSettledTopic, "trust", func(ctx context.Context, event order.OrderSettled) error {
		req := trustapi.RecordOrderOutcomeRequest{
			BuyerID:   id.Of[id.Account](event.BuyerID),
			SellerID:  id.Of[id.Account](event.SellerID),
			Completed: event.Completed,
		}
		if err := svc.RecordOrderOutcome(ctx, req); err != nil {
			log.Error("record order outcome failed", "order_id", event.OrderID, "err", err)
			return err
		}
		return nil
	})
}
