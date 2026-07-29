package common

import (
	"context"
	"fmt"

	"go.uber.org/fx"

	"shopnexus/internal/config"
	"shopnexus/internal/infra/postgres"
	commonpg "shopnexus/internal/module/common/adapter/postgres"
	commonapi "shopnexus/internal/module/common/api"
	"shopnexus/internal/module/common/port"
)

// Module wires the common service and its Postgres-backed repository.
var Module = fx.Module("common",
	fx.Provide(
		fx.Annotate(newRepo, fx.As(new(port.Repository))),
		fx.Annotate(NewService, fx.As(new(commonapi.Service))),
	),
)

func newRepo(lc fx.Lifecycle, cfg *config.Config) (*commonpg.Repo, error) {
	pool, err := postgres.NewPool(context.Background(), cfg.CommonDBDSN, "common")
	if err != nil {
		return nil, fmt.Errorf("open common db pool: %w", err)
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error { pool.Close(); return nil }})
	return commonpg.New(pool), nil
}
