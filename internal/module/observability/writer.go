package observability

import (
	"time"

	"shopnexus/internal/infra/eventbus"
	"shopnexus/internal/module/observability/port"
)

// writerGroup is the consumer group draining telemetry into the repository. One
// group means the work is shared, not duplicated, once several gateways run.
const writerGroup = "observability-writer"

// Batch shape per signal: a batch flushes at its size or after the linger,
// whichever comes first. HTTP and provider calls are the high-volume signals;
// runtime samples arrive once per sampling interval, so they take a small batch
// rather than sitting in the stream.
const (
	writerLinger      = 2 * time.Second
	httpBatchSize     = 1000
	providerBatchSize = 500
	eventBatchSize    = 100
	runtimeBatchSize  = 10
)

// subscribeWriter drains every telemetry topic into the repository, one batching
// consumer per signal. The repository's Insert* signature is exactly the batch
// handler shape, so each subscription is a straight hand-off. A failed insert
// returns an error, which nacks the batch and gets it redelivered.
//
// The parameter is the concrete *eventbus.NATS, never eventbus.Client: the
// domain event bus (Redis) also satisfies that interface, and fx would happily
// inject it — the writer would then listen on a bus nobody publishes telemetry to.
func subscribeWriter(bus *eventbus.NATS, repo port.Repository) {
	eventbus.SubscribeBatch(bus, httpTopic, writerGroup, repo.InsertHTTPRequests,
		eventbus.WithBatchSize(httpBatchSize), eventbus.WithLinger(writerLinger))

	eventbus.SubscribeBatch(bus, providerTopic, writerGroup, repo.InsertProviderCalls,
		eventbus.WithBatchSize(providerBatchSize), eventbus.WithLinger(writerLinger))

	eventbus.SubscribeBatch(bus, eventTopic, writerGroup, repo.InsertBusinessEvents,
		eventbus.WithBatchSize(eventBatchSize), eventbus.WithLinger(writerLinger))

	eventbus.SubscribeBatch(bus, runtimeTopic, writerGroup, repo.InsertRuntimeMetrics,
		eventbus.WithBatchSize(runtimeBatchSize), eventbus.WithLinger(writerLinger))
}
