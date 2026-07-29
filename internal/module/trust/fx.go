package trust

import (
	"context"
	"fmt"

	"go.uber.org/fx"

	"shopnexus/internal/config"
	"shopnexus/internal/infra/postgres"
	trustpg "shopnexus/internal/module/trust/adapter/postgres"
	trustapi "shopnexus/internal/module/trust/api"
	"shopnexus/internal/module/trust/port"
)

// Module wires the trust service and its Postgres-backed repository.
var Module = fx.Module("trust",
	fx.Provide(
		fx.Annotate(newRepo, fx.As(new(port.Repository))),
		fx.Annotate(NewService, fx.As(new(trustapi.Service))),
	),
)

func newRepo(lc fx.Lifecycle, cfg *config.Config) (*trustpg.Repo, error) {
	pool, err := postgres.NewPool(context.Background(), cfg.TrustDBDSN, "trust")
	if err != nil {
		return nil, fmt.Errorf("open trust db pool: %w", err)
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error { pool.Close(); return nil }})
	return trustpg.New(pool), nil
}
