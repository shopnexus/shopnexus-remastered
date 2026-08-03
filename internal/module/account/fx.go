package account

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"

	"shopnexus/internal/config"
	"shopnexus/internal/infra/durable"
	"shopnexus/internal/infra/postgres"
	accountpg "shopnexus/internal/module/account/adapter/postgres"
	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/port"
	"shopnexus/internal/module/common"
	"shopnexus/internal/module/common/dbx"
	"shopnexus/internal/module/common/uploads"
	"shopnexus/internal/provider/storage"
)

// Module wires the account service, its Postgres-backed repository, and the uploads an
// avatar or an identity scan lands in.
//
// Everything else the service needs is provided at the app level and resolved by
// interface: the session store, the cache, the notify/oauth/kyc providers and the
// common module's service.
var Module = fx.Module("account",
	// Private, and in a Provide of its own because fx.Private applies to every constructor in
	// the same call. All three are this module's own: two modules each providing a bare
	// *pgxpool.Pool, a bare *uploads.Store or a bare common.Uploads into the root graph is a
	// conflict rather than one of each per module — only this module's own service may see them.
	fx.Provide(fx.Private,
		newPool,
		newUploads,
		fx.Annotate(func(s *uploads.Store) common.Uploads { return s }),
	),
	fx.Provide(
		fx.Annotate(newRepo, fx.As(new(port.Repository))),
		fx.Annotate(newUploadSweep, fx.ResultTags(`group:"sweeps"`)),
		fx.Annotate(NewService, fx.As(new(accountapi.Service))),
	),
	// Eager, because nothing else in the graph depends on a subscription: without it the bus
	// would have no consumer and an order fact would never become a feed row.
	fx.Invoke(SubscribeOrderEvents),
)

func newPool(lc fx.Lifecycle, cfg *config.Config) (*pgxpool.Pool, error) {
	pool, err := postgres.NewPool(context.Background(), cfg.AccountDBDSN, "account")
	if err != nil {
		return nil, fmt.Errorf("open account db pool: %w", err)
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error { pool.Close(); return nil }})
	return pool, nil
}

func newRepo(pool *pgxpool.Pool) *accountpg.Repo { return accountpg.New(pool) }

// newUploads is this module's own `resource` rows plus the object store. One store serves
// both an avatar and an identity scan — the kind only narrows the key prefix, and what keeps
// a scan from being world-readable is that Resolve always signs a fresh, short-lived link
// rather than handing back a public URL.
func newUploads(pool *pgxpool.Pool, cfg *config.Config, stores *storage.Registry) *uploads.Store {
	return uploads.New(dbx.NewResources(pool), stores, "account", cfg.StorageUploadTTL)
}

// newUploadSweep reaps the slots nobody confirmed, so an abandoned upload is not a row and an
// object that accumulate for ever.
func newUploadSweep(store *uploads.Store) durable.Sweep { return store.Sweep }
