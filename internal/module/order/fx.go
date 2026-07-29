package order

import (
	"context"
	"fmt"

	"go.uber.org/fx"

	"shopnexus/internal/config"
	"shopnexus/internal/infra/postgres"
	orderpg "shopnexus/internal/module/order/adapter/postgres"
	orderapi "shopnexus/internal/module/order/api"
	"shopnexus/internal/module/order/port"
)

// Module wires the order service and its Postgres-backed repository.
var Module = fx.Module("order",
	fx.Provide(
		fx.Annotate(newRepo, fx.As(new(port.Repository))),
		fx.Annotate(NewService, fx.As(new(orderapi.Service))),
	),
)

func newRepo(lc fx.Lifecycle, cfg *config.Config) (*orderpg.Repo, error) {
	pool, err := postgres.NewPool(context.Background(), cfg.OrderDBDSN, "order")
	if err != nil {
		return nil, fmt.Errorf("open order db pool: %w", err)
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error { pool.Close(); return nil }})
	return orderpg.New(pool), nil
}
