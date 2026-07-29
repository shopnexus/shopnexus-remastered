package catalog

import (
	"context"
	"fmt"

	"go.uber.org/fx"

	"shopnexus/internal/config"
	"shopnexus/internal/infra/postgres"
	catalogpg "shopnexus/internal/module/catalog/adapter/postgres"
	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/module/catalog/port"
)

// Module wires the catalog service and its Postgres-backed repository.
var Module = fx.Module("catalog",
	fx.Provide(
		fx.Annotate(newRepo, fx.As(new(port.Repository))),
		fx.Annotate(NewService, fx.As(new(catalogapi.Service))),
	),
)

func newRepo(lc fx.Lifecycle, cfg *config.Config) (*catalogpg.Repo, error) {
	pool, err := postgres.NewPool(context.Background(), cfg.CatalogDBDSN, "catalog")
	if err != nil {
		return nil, fmt.Errorf("open catalog db pool: %w", err)
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error { pool.Close(); return nil }})
	return catalogpg.New(pool), nil
}
