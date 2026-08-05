package finance

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"

	"shopnexus/internal/config"
	"shopnexus/internal/infra/postgres"
	"shopnexus/internal/module/common"
	"shopnexus/internal/module/common/dbx"
	financepg "shopnexus/internal/module/finance/adapter/postgres"
	financeapi "shopnexus/internal/module/finance/api"
	"shopnexus/internal/module/finance/port"
	"shopnexus/internal/provider/payment"
)

// Module wires the finance service, its Postgres-backed repository, the payment-option
// registry it reads from its own schema, and the provider webhook that settles a leg.
var Module = fx.Module("finance",
	// Private, and in a Provide of its own because fx.Private applies to every constructor
	// in the same call: the pool is this module's own, and two modules each providing a bare
	// *pgxpool.Pool into the root graph is a conflict rather than two pools.
	// The option store is private for the same reason: `common.OptionStore` is one type and every
	// module has its own rows, so providing it into the root graph is a conflict rather than one
	// store per module.
	fx.Provide(fx.Private,
		newPool,
		fx.Annotate(newOptions, fx.As(new(common.OptionStore))),
	),
	fx.Provide(
		fx.Annotate(newRepo, fx.As(new(port.Repository))),
		newReturnURLHosts,
		fx.Annotate(NewService, fx.As(new(financeapi.Service))),
	),
	// Both eager, because nothing else in the graph depends on a mounted route or a written row:
	// without these the rails would exist only once something happened to ask for the service.
	fx.Invoke(SyncOptions),
	fx.Invoke(WireWebhooks),
)

// newPool is separate from the repo so the option store can share it: the registry is
// this module's own `option` rows, in this module's schema.
func newPool(lc fx.Lifecycle, cfg *config.Config) (*pgxpool.Pool, error) {
	pool, err := postgres.NewPool(context.Background(), cfg.FinanceDBDSN, "finance")
	if err != nil {
		return nil, fmt.Errorf("open finance db pool: %w", err)
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error { pool.Close(); return nil }})
	return pool, nil
}

func newRepo(pool *pgxpool.Pool) *financepg.Repo { return financepg.New(pool) }

// newReturnURLHosts is the allowlist a payer's redirect target has to be in. Configuration
// rather than code: the hosts differ per deployment, and an unchecked target is an open
// redirect wearing a payment flow.
func newReturnURLHosts(cfg *config.Config) ReturnURLHosts {
	return ReturnURLHosts(cfg.PaymentReturnURLHosts)
}

func newOptions(pool *pgxpool.Pool) *dbx.Options { return dbx.NewOptions(pool) }

// SyncOptions writes the rows every registered rail owns, before the server accepts traffic: the
// list a client fetches has to be the one this binary can serve. A failure is fatal — a checkout
// with no rails is not a degraded deployment, it is one that cannot take money.
func SyncOptions(svc financeapi.Service) error {
	syncer, ok := svc.(*Service)
	if !ok {
		return nil
	}
	if err := syncer.SyncOptions(context.Background()); err != nil {
		return fmt.Errorf("sync payment options: %w", err)
	}
	return nil
}

// WireWebhooks mounts every registered rail's IPN route and hands each the settler. Per provider,
// not per option row: the callback path belongs to the vendor, and two rails that share one would
// be settling each other's notifications.
func WireWebhooks(mux *http.ServeMux, rails *common.Registry[payment.Client], svc financeapi.Service, log *slog.Logger) {
	settler, ok := svc.(*Service)
	if !ok {
		return
	}
	_ = rails.Each(func(provider string, rail payment.Client) error {
		path := rail.WireWebhooks(mux, func(ctx context.Context, n payment.Notification) error {
			return settler.Settle(ctx, n)
		})
		log.Info("payment webhook mounted", "provider", provider, "path", path)
		return nil
	})
}
