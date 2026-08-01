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
)

// Module wires the HTTP handlers, the router, and the HTTP server lifecycle.
// Handler constructors consume the per-module api.Service interfaces provided
// by the domain modules; fx resolves them.
var Module = fx.Module("gateway",
	fx.Provide(
		handler.NewAccount,
		handler.NewCatalog,
		handler.NewChat,
		handler.NewFinance,
		handler.NewOrder,
		handler.NewTrust,
		// One mux for every provider callback. Provided here rather than by a provider,
		// because several of them mount on it and the router mounts the result.
		newWebhookMux,
		newRouter,
	),
	fx.Invoke(startServer),
)

// newWebhookMux is where a provider's IPN routes land. Outside the versioned API and
// outside auth: a gateway calls the URL it was configured with and has no token.
func newWebhookMux() *http.ServeMux { return http.NewServeMux() }

func newRouter(d Deps) http.Handler { return NewRouter(d) }

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
