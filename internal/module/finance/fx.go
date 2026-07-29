package finance

import (
	"context"
	"fmt"

	"go.uber.org/fx"

	"shopnexus/internal/config"
	"shopnexus/internal/infra/postgres"
	financepg "shopnexus/internal/module/finance/adapter/postgres"
	financeapi "shopnexus/internal/module/finance/api"
	"shopnexus/internal/module/finance/port"
)

// Module wires the finance service and its Postgres-backed repository.
var Module = fx.Module("finance",
	fx.Provide(
		fx.Annotate(newRepo, fx.As(new(port.Repository))),
		fx.Annotate(NewService, fx.As(new(financeapi.Service))),
	),
)

func newRepo(lc fx.Lifecycle, cfg *config.Config) (*financepg.Repo, error) {
	pool, err := postgres.NewPool(context.Background(), cfg.FinanceDBDSN, "finance")
	if err != nil {
		return nil, fmt.Errorf("open finance db pool: %w", err)
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error { pool.Close(); return nil }})
	return financepg.New(pool), nil
}
