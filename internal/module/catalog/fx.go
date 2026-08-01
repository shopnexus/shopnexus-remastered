package catalog

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"

	"shopnexus/internal/config"
	"shopnexus/internal/infra/postgres"
	catalogpg "shopnexus/internal/module/catalog/adapter/postgres"
	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/module/catalog/port"
	"shopnexus/internal/module/common"
	"shopnexus/internal/module/common/dbx"
	"shopnexus/internal/module/common/uploads"
	"shopnexus/internal/provider/storage"
)

// Module wires the catalog service, its Postgres-backed repository, and the uploads its
// listings' photos land in.
var Module = fx.Module("catalog",
	// Private, and in a Provide of its own because fx.Private applies to every constructor in
	// the same call: the pool is this module's own, and two modules each providing a bare
	// *pgxpool.Pool into the root graph is a conflict rather than two pools.
	fx.Provide(fx.Private, newPool),
	fx.Provide(
		fx.Annotate(newRepo, fx.As(new(port.Repository))),
		fx.Annotate(newUploads, fx.As(new(common.Uploads))),
		fx.Annotate(NewService, fx.As(new(catalogapi.Service))),
	),
)

func newPool(lc fx.Lifecycle, cfg *config.Config) (*pgxpool.Pool, error) {
	pool, err := postgres.NewPool(context.Background(), cfg.CatalogDBDSN, "catalog")
	if err != nil {
		return nil, fmt.Errorf("open catalog db pool: %w", err)
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error { pool.Close(); return nil }})
	return pool, nil
}

func newRepo(pool *pgxpool.Pool) *catalogpg.Repo { return catalogpg.New(pool) }

// newUploads is this module's own `resource` rows plus the object store. The prefix keeps
// catalog's objects together, so an operator holding only a key can tell what it belongs to.
func newUploads(pool *pgxpool.Pool, client storage.Client) *uploads.Store {
	return uploads.New(dbx.NewResources(pool), client, "catalog")
}
