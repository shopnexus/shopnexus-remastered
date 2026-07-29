package observability

import (
	"context"
	"fmt"
	"log/slog"

	"go.uber.org/fx"

	"shopnexus/internal/config"
	"shopnexus/internal/infra/eventbus"
	"shopnexus/internal/infra/postgres"
	obspg "shopnexus/internal/module/observability/adapter/postgres"
	"shopnexus/internal/module/observability/port"
)

// Module wires the telemetry pipeline: the Sink publishes samples to JetStream,
// the writer drains each topic into the repository in batches, the runtime
// sampler feeds it periodically, and business events are mirrored off the domain
// bus. The gateway wraps its router with Sink.Middleware for HTTP RED metrics.
// There is no api/ package: nothing calls this module, it is driven by the
// middleware, the sampler and the bus.
var Module = fx.Module("observability",
	fx.Provide(
		fx.Annotate(newRepo, fx.As(new(port.Repository))),
		newTelemetryBus,
		newSink,
	),
	fx.Invoke(subscribeWriter),
	// Registered before startSampler so shutdown runs in reverse: the sampler
	// stops publishing, then the bus closes, then the repository's pool.
	fx.Invoke(stopTelemetryBus),
	fx.Invoke(startSampler),
	fx.Invoke(subscribeEvents),
)

func newRepo(lc fx.Lifecycle, cfg *config.Config) (*obspg.Repo, error) {
	pool, err := postgres.NewPool(context.Background(), cfg.ObservabilityDBDSN, "observability")
	if err != nil {
		return nil, fmt.Errorf("open observability db pool: %w", err)
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error { pool.Close(); return nil }})
	return obspg.New(pool), nil
}

// newTelemetryBus dials the JetStream bus that buffers telemetry. It is its own
// connection, separate from the domain event bus, and is typed concretely so fx
// never confuses the two eventbus.Client implementations.
//
// Its OnStop lives in stopTelemetryBus (an fx.Invoke) rather than here: invokes
// run after every constructor, so their hooks are registered last and therefore
// stop first — consumers shut down before the pool they write to.
func newTelemetryBus(cfg *config.Config, log *slog.Logger) (*eventbus.NATS, error) {
	bus, err := eventbus.DialNATS(context.Background(), cfg.NATSURL, log)
	if err != nil {
		return nil, fmt.Errorf("dial telemetry bus: %w", err)
	}
	return bus, nil
}

// newSink exists next to NewSink so the graph can pick the right bus: NewSink
// takes the eventbus.Client interface (both buses satisfy it, so fx could not
// tell them apart), while this asks for the concrete *eventbus.NATS.
func newSink(bus *eventbus.NATS, log *slog.Logger, cfg *config.Config) *Sink {
	return NewSink(bus, log, cfg.InstanceID)
}

func startSampler(lc fx.Lifecycle, s *Sink) {
	ctx, cancel := context.WithCancel(context.Background())
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error { go s.SampleLoop(ctx); return nil },
		OnStop:  func(context.Context) error { cancel(); return nil },
	})
}

// stopTelemetryBus closes the bus on shutdown. Samples already in the stream are
// not lost: the durable consumers resume after restart.
func stopTelemetryBus(lc fx.Lifecycle, bus *eventbus.NATS) {
	lc.Append(fx.Hook{OnStop: func(context.Context) error {
		if err := bus.Close(); err != nil {
			return fmt.Errorf("close telemetry bus: %w", err)
		}
		return nil
	}})
}
