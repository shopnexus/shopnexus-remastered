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
	fx.Provide(fx.Private, newPool),
	fx.Provide(
		fx.Annotate(newRepo, fx.As(new(port.Repository))),
		fx.Annotate(newOptions, fx.As(new(port.Options))),
		newReturnURLHosts,
		newRailProvider,
		fx.Annotate(NewService, fx.As(new(financeapi.Service))),
	),
	// The service is built eagerly and its webhook mounted, because nothing else in the
	// graph depends on the mount: without this the routes would only exist once some
	// other component happened to ask for the service.
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

// newRailProvider is which vendor this deployment charges through, so the registry can offer only
// the rows that vendor can serve.
func newRailProvider(cfg *config.Config) RailProvider { return RailProvider(cfg.PaymentProvider) }

// WireWebhooks mounts the payment provider's IPN routes and hands it the settler. The
// webhook is the provider's own path, not one of ours: a gateway calls the URL it was
// configured with, and this is where that URL starts existing.
func WireWebhooks(mux *http.ServeMux, gateway payment.Client, svc financeapi.Service, log *slog.Logger) {
	settler, ok := svc.(*Service)
	if !ok {
		return
	}
	path := gateway.WireWebhooks(mux, func(ctx context.Context, n payment.Notification) error {
		return settler.Settle(ctx, n)
	})
	log.Info("payment webhook mounted", "path", path)
}
