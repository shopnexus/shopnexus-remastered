package observability

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"shopnexus/internal/infra/eventbus"
	"shopnexus/internal/module/observability/domain"
)

// The Sink publishes; the writer consumes. These tests drive that contract over
// the in-memory bus, so no NATS or database is needed.

func TestSink_RecordHTTPPublishesSample(t *testing.T) {
	bus := eventbus.NewMemory(slog.Default())
	t.Cleanup(func() { _ = bus.Close() })

	received := make(chan []domain.HTTPSample, 1)
	eventbus.SubscribeBatch(bus, httpTopic, "test", func(_ context.Context, samples []domain.HTTPSample) error {
		received <- samples
		return nil
	})

	NewSink(bus, slog.Default(), "test-instance").RecordHTTP("GET", "GET /listings/{id}", 200, 1500*time.Microsecond)

	select {
	case batch := <-received:
		if len(batch) != 1 {
			t.Fatalf("batch size = %d, want 1", len(batch))
		}
		got := batch[0]
		if got.Method != "GET" || got.Route != "GET /listings/{id}" || got.Status != 200 {
			t.Errorf("unexpected sample: %+v", got)
		}
		if got.DurationMs != 1.5 {
			t.Errorf("duration_ms = %v, want 1.5", got.DurationMs)
		}
		if got.TS.IsZero() {
			t.Error("ts not set")
		}
		// The column is NOT NULL, so an unstamped sample fails the COPY.
		if got.Instance != "test-instance" {
			t.Errorf("instance = %q, want %q", got.Instance, "test-instance")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no sample delivered")
	}
}

// Batching is the bus's job (BatchSize + Linger), which is what the writer
// relies on to COPY many rows per insert.
func TestSink_SamplesArriveBatched(t *testing.T) {
	bus := eventbus.NewMemory(slog.Default())
	t.Cleanup(func() { _ = bus.Close() })

	batches := make(chan int, 4)
	eventbus.SubscribeBatch(bus, httpTopic, "test", func(_ context.Context, samples []domain.HTTPSample) error {
		batches <- len(samples)
		return nil
	}, eventbus.WithBatchSize(3), eventbus.WithLinger(time.Second))

	s := NewSink(bus, slog.Default(), "test-instance")
	for range 3 {
		s.RecordHTTP("GET", "/x", 200, time.Millisecond)
	}

	select {
	case n := <-batches:
		if n != 3 {
			t.Fatalf("batch size = %d, want 3 (BatchSize reached before Linger)", n)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no batch delivered")
	}
}

func TestSink_RecordEventKeepsPayload(t *testing.T) {
	bus := eventbus.NewMemory(slog.Default())
	t.Cleanup(func() { _ = bus.Close() })

	received := make(chan domain.BusinessEvent, 1)
	eventbus.Subscribe(bus, eventTopic, "test", func(_ context.Context, sample domain.BusinessEvent) error {
		received <- sample
		return nil
	})

	NewSink(bus, slog.Default(), "test-instance").RecordEvent("order.placed", []byte(`{"order_id":"ord-1"}`))

	select {
	case got := <-received:
		if got.Topic != "order.placed" {
			t.Errorf("topic = %q, want order.placed", got.Topic)
		}
		if string(got.Payload) != `{"order_id":"ord-1"}` {
			t.Errorf("payload = %s, want the raw event JSON", got.Payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event delivered")
	}
}

// A bus outage must not surface to the caller: the sample is counted and dropped.
func TestSink_CountsDroppedSamples(t *testing.T) {
	bus := eventbus.NewMemory(slog.Default())
	if err := bus.Close(); err != nil {
		t.Fatalf("close bus: %v", err)
	}

	s := NewSink(bus, slog.Default(), "test-instance")
	s.RecordHTTP("GET", "/x", 200, time.Millisecond)
	s.RecordRuntime(1, 2, 3, 0.5, 4)

	if got := s.dropped.Load(); got != 2 {
		t.Fatalf("dropped = %d, want 2", got)
	}
	s.reportDropped()
	if got := s.dropped.Load(); got != 0 {
		t.Fatalf("dropped after report = %d, want 0", got)
	}
}
