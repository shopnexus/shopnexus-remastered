package order

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"

	"shopnexus/internal/config"
	"shopnexus/internal/infra/eventbus"
	"shopnexus/internal/infra/postgres"
	"shopnexus/internal/module/common/dbx"
	finance "shopnexus/internal/module/finance"
	orderpg "shopnexus/internal/module/order/adapter/postgres"
	orderapi "shopnexus/internal/module/order/api"
	"shopnexus/internal/module/order/port"
	"shopnexus/internal/shared/id"
)

// Module wires the order service, its repository, the carrier registry it reads from its own
// schema, the durable lifecycle, and the subscriber that turns a settled payment into an
// order.
var Module = fx.Module("order",
	fx.Provide(
		newPool,
		fx.Annotate(newRepo, fx.As(new(port.Repository))),
		fx.Annotate(newOptions, fx.As(new(port.Options))),
		fx.Annotate(NewService, fx.As(new(orderapi.Service))),
		NewLifecycle,
	),
	// Eager, because nothing else in the graph depends on a subscription: without this the
	// bus would have no consumer until something happened to ask for the service.
	fx.Invoke(SubscribePaidSessions),
)

func newPool(lc fx.Lifecycle, cfg *config.Config) (*pgxpool.Pool, error) {
	pool, err := postgres.NewPool(context.Background(), cfg.OrderDBDSN, "order")
	if err != nil {
		return nil, fmt.Errorf("open order db pool: %w", err)
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error { pool.Close(); return nil }})
	return pool, nil
}

func newRepo(pool *pgxpool.Pool) *orderpg.Repo { return orderpg.New(pool) }

func newOptions(pool *pgxpool.Pool) *dbx.Options { return dbx.NewOptions(pool) }

// SubscribePaidSessions is what makes the money create the order. Finance publishes a
// settled session; this turns it into an order, a shipment and an escrow hold.
//
// The handler is idempotent, so a redelivered message is a no-op rather than a second order —
// which is what lets the bus be at-least-once and the durable workflow retry the same step.
func SubscribePaidSessions(bus eventbus.Client, svc orderapi.Service, log *slog.Logger) {
	eventbus.Subscribe(bus, finance.SessionPaidTopic, "order", func(ctx context.Context, event finance.SessionPaid) error {
		// Only a buyer's checkout becomes an order. A payout or a withdrawal is finance's own
		// business and has no sale behind it.
		if event.Kind != "buyer-checkout" {
			return nil
		}
		if err := svc.SettlePaidSession(ctx, id.Of[id.PaymentSession](event.SessionID)); err != nil {
			log.Error("settle paid session failed", "session_id", event.SessionID, "err", err)
			return err
		}
		return nil
	})
}
