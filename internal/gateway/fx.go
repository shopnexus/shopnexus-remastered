package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"

	"go.uber.org/fx"

	"shopnexus/internal/config"
	"shopnexus/internal/gateway/handler"
	"shopnexus/internal/module/observability"
	"shopnexus/internal/shared/token"
)

// Module wires the HTTP handlers, the router, and the HTTP server lifecycle.
// Handler constructors consume the per-module api.Service interfaces provided
// by the domain modules; fx resolves them.
var Module = fx.Module("gateway",
	fx.Provide(
		handler.NewAccount,
		handler.NewCatalog,
		handler.NewChat,
		handler.NewCommon,
		handler.NewFinance,
		handler.NewOrder,
		handler.NewTrust,
		newRouter,
	),
	fx.Invoke(startServer),
)

type routerParams struct {
	fx.In
	Account *handler.Account
	Catalog *handler.Catalog
	Chat    *handler.Chat
	Common  *handler.Common
	Finance *handler.Finance
	Order   *handler.Order
	Trust   *handler.Trust
	Metrics *observability.Sink
	Tokens  *token.Manager
	Log     *slog.Logger
}

func newRouter(p routerParams) http.Handler {
	return NewRouter(Deps{
		Account: p.Account,
		Catalog: p.Catalog,
		Chat:    p.Chat,
		Common:  p.Common,
		Finance: p.Finance,
		Order:   p.Order,
		Trust:   p.Trust,
		Metrics: p.Metrics,
		Tokens:  p.Tokens,
		Log:     p.Log,
	})
}

func startServer(lc fx.Lifecycle, cfg *config.Config, h http.Handler, log *slog.Logger) {
	srv := &http.Server{Addr: cfg.GatewayAddr, Handler: h}
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			ln, err := net.Listen("tcp", cfg.GatewayAddr)
			if err != nil {
				return fmt.Errorf("listen on %s: %w", cfg.GatewayAddr, err)
			}
			log.Info("gateway starting", "addr", cfg.GatewayAddr)
			go func() {
				if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
					log.Error("server error", "err", err)
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			return srv.Shutdown(ctx)
		},
	})
}
