package account

import (
	"context"
	"fmt"

	"go.uber.org/fx"

	"shopnexus/internal/config"
	"shopnexus/internal/infra/postgres"
	accountpg "shopnexus/internal/module/account/adapter/postgres"
	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/port"
)

// Module wires the account service and its Postgres-backed repository.
//
// Everything else the service needs is provided at the app level and resolved by
// interface: the session store, the cache, the notify/oauth/kyc providers and the
// common module's service.
var Module = fx.Module("account",
	fx.Provide(
		fx.Annotate(newRepo, fx.As(new(port.Repository))),
		fx.Annotate(NewService, fx.As(new(accountapi.Service))),
	),
)

func newRepo(lc fx.Lifecycle, cfg *config.Config) (*accountpg.Repo, error) {
	pool, err := postgres.NewPool(context.Background(), cfg.AccountDBDSN, "account")
	if err != nil {
		return nil, fmt.Errorf("open account db pool: %w", err)
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error { pool.Close(); return nil }})
	return accountpg.New(pool), nil
}
