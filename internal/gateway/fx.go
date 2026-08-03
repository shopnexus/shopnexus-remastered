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
	"shopnexus/internal/gateway/ws"
	"shopnexus/internal/infra/eventbus"
	"shopnexus/internal/module/observability"
	"shopnexus/internal/shared/session"
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
		newHub,
		newWSHandler,
		// observability must not import this package, so the connection count crosses
		// through a func rather than the hub itself; provided here where both types are
		// in scope.
		newConnCounter,
		// One mux for every provider callback. Provided here rather than by a provider,
		// because several of them mount on it and the router mounts the result.
		newWebhookMux,
		newRouter,
	),
	fx.Invoke(startServer),
)

// newHub takes the concrete *eventbus.NATS, not eventbus.Client. Both buses satisfy
// Client, so asking for the interface would silently wire the socket to the Redis bus,
// which has no Broadcast at all — the same mix-up telemetry's newSink guards against in
// reverse.
func newHub(f *eventbus.NATS, log *slog.Logger, cfg *config.Config) *ws.Hub {
	return ws.NewHub(f, log, ws.Config{
		SendBuffer:    cfg.WSSendBuffer,
		MaxPerAccount: cfg.WSMaxPerAccount,
	})
}

func newWSHandler(hub *ws.Hub, tickets *session.Tickets, sessions *session.Store, log *slog.Logger, cfg *config.Config) *handler.WS {
	return handler.NewWS(hub, tickets, sessions, log,
		cfg.WSWriteTimeout, cfg.WSPingInterval, cfg.WSAllowedOrigins)
}

// newConnCounter feeds the hub's live socket count into the runtime sampler.
func newConnCounter(hub *ws.Hub) observability.ConnCounter { return hub.Count }

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
