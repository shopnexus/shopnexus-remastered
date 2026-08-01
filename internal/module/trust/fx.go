package trust

import (
	"context"
	"fmt"
	"log/slog"

	"go.uber.org/fx"

	"shopnexus/internal/config"
	"shopnexus/internal/infra/durable"
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
		fx.Annotate(newSweep, fx.ResultTags(`group:"sweeps"`)),
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
// Order is the authority and this is a mirror. Delivery is at-least-once, so the order id
// travels with the outcome and the service records it in the same transaction as the bump: a
// redelivery counts nothing, and a message that never arrived is still the one gap a recount
// has to close.
func SubscribeSettledOrders(bus eventbus.Client, svc trustapi.Service, log *slog.Logger) {
	eventbus.Subscribe(bus, order.OrderSettledTopic, "trust", func(ctx context.Context, event order.OrderSettled) error {
		req := trustapi.RecordOrderOutcomeRequest{
			OrderID:   id.Of[id.Order](event.OrderID),
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

// newSweep registers the blind-window pass with the shared sweeper: one interval for every
// module's catch-up work rather than a ticker each.
func newSweep(r *Reveal) durable.Sweep { return r.Sweep }
