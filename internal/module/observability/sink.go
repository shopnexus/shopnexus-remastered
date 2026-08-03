package observability

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"shopnexus/internal/infra/eventbus"
	"shopnexus/internal/module/observability/domain"
	"shopnexus/internal/shared/httpx"
)

// Telemetry topics. Each signal is published to the event bus (JetStream) and
// drained into its hypertable by the writer, which consumes with a batch size
// and a linger — so batching lives in the bus, not in this process's memory.
var (
	httpTopic     = eventbus.NewTopic[domain.HTTPSample]("telemetry.http_requests")
	providerTopic = eventbus.NewTopic[domain.ProviderCall]("telemetry.provider_calls")
	eventTopic    = eventbus.NewTopic[domain.BusinessEvent]("telemetry.business_events")
	runtimeTopic  = eventbus.NewTopic[domain.RuntimeSample]("telemetry.runtime_metrics")
)

// wsPath is the realtime socket's route, seen here without the API base path — the
// router mounts paths unprefixed and strips the prefix before this middleware runs. A
// socket held for minutes would otherwise enter http_requests_1m as a minutes-long
// request and make approx_percentile(0.95, "latency") meaningless; Middleware excludes
// it and the connection count is sampled separately (see ConnCounter).
const wsPath = "/ws"

// Sink records telemetry by publishing samples to the event bus; the writer
// batches them into TimescaleDB. Publishing is asynchronous and best-effort, so
// the request path never waits on the bus or the database — and because
// JetStream persists a sample once it lands, a database outage no longer loses
// telemetry: the writer's batch is redelivered.
// The instance is a property of this process, so it is stamped here rather than
// threaded through every Record call site.
type Sink struct {
	bus      eventbus.Client
	log      *slog.Logger
	instance string
	dropped  atomic.Uint64
}

func NewSink(bus eventbus.Client, log *slog.Logger, instance string) *Sink {
	return &Sink{bus: bus, log: log, instance: instance}
}

// RecordHTTP publishes one inbound request observation.
func (s *Sink) RecordHTTP(method, route string, status int, d time.Duration) {
	publish(s, httpTopic, domain.HTTPSample{
		TS:         time.Now(),
		Instance:   s.instance,
		Method:     method,
		Route:      route,
		Status:     status,
		DurationMs: millis(d),
	})
}

// RecordProviderCall publishes one outbound dependency call.
func (s *Sink) RecordProviderCall(call httpx.OutboundCall) {
	var errText string
	if call.Err != nil {
		errText = domain.CapError(call.Err.Error())
	}
	publish(s, providerTopic, domain.ProviderCall{
		TS:         time.Now(),
		Instance:   s.instance,
		Provider:   call.Provider,
		Method:     call.Method,
		Path:       call.Path,
		Status:     call.StatusCode,
		DurationMs: millis(call.Duration),
		Failed:     call.Failed(),
		Error:      errText,
	})
}

// RecordEvent publishes a business event (raw JSON payload from the bus).
func (s *Sink) RecordEvent(topic string, payload []byte) {
	publish(s, eventTopic, domain.BusinessEvent{
		TS:       time.Now(),
		Instance: s.instance,
		Topic:    topic,
		Payload:  payload,
	})
}

// RecordRuntime publishes one Go runtime sample.
func (s *Sink) RecordRuntime(goroutines int, heapAlloc, heapInuse uint64, gcPauseMs float64, numGC uint64, wsConns int) {
	publish(s, runtimeTopic, domain.RuntimeSample{
		TS:             time.Now(),
		Instance:       s.instance,
		Goroutines:     goroutines,
		HeapAllocBytes: int64(heapAlloc),
		HeapInuseBytes: int64(heapInuse),
		GCPauseMs:      gcPauseMs,
		NumGC:          int64(numGC),
		WebSocketConns: wsConns,
	})
}

// OutboundObserver adapts the Sink to httpx's outbound hook, so any provider's
// http.Client reports into provider_calls:
//
//	&http.Client{Transport: httpx.ObserveOutbound("litellm", nil, sink.OutboundObserver())}
func (s *Sink) OutboundObserver() httpx.OutboundObserver {
	return func(_ context.Context, call httpx.OutboundCall) {
		s.RecordProviderCall(call)
	}
}

// publish sends a sample to its topic. context.Background is deliberate: a
// cancelled request must not drop the observation of that request.
func publish[T any](s *Sink, topic eventbus.Topic[T], sample T) {
	if err := eventbus.Publish(context.Background(), s.bus, topic, sample); err != nil {
		// Best-effort: count the loss instead of logging once per sample, and
		// never fail the caller. reportDropped surfaces the total.
		s.dropped.Add(1)
	}
}

// reportDropped logs and resets the number of samples that never reached the
// bus. Called from the runtime sampler so a bus outage is visible without
// logging on every request.
func (s *Sink) reportDropped() {
	if n := s.dropped.Swap(0); n > 0 {
		s.log.Warn("observability samples dropped", "count", n)
	}
}

func millis(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000
}
