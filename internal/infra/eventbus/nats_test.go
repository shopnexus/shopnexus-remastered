//go:build integration

package eventbus_test

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"shopnexus/internal/infra/eventbus"
)

func natsBus(t *testing.T) *eventbus.NATS {
	t.Helper()
	url := os.Getenv("NATS_URL")
	if url == "" {
		t.Skip("NATS_URL not set")
	}
	b, err := eventbus.DialNATS(context.Background(), url, slog.Default())
	if err != nil {
		t.Fatalf("dial nats: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b
}

// uniqueName keeps each run on its own subject + durable consumer, so a previous
// run's backlog cannot leak into this one.
func uniqueName(prefix string) string {
	return prefix + strconv.FormatInt(time.Now().UnixNano(), 36)
}

// Fetch(BatchSize, MaxWait(Linger)) must hand the handler one full batch.
func TestNATS_DeliversFullBatch(t *testing.T) {
	b := natsBus(t)
	topic := eventbus.NewTopic[int](uniqueName("telemetry.test_batch_"))

	batches := make(chan []int, 4)
	eventbus.SubscribeBatch(b, topic, uniqueName("g_"), func(_ context.Context, payloads []int) error {
		batches <- payloads
		return nil
	}, eventbus.WithBatchSize(3), eventbus.WithLinger(2*time.Second))

	for i := range 3 {
		if err := eventbus.Publish(context.Background(), b, topic, i); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}

	select {
	case got := <-batches:
		if len(got) != 3 {
			t.Fatalf("batch size = %d, want 3", len(got))
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no batch delivered")
	}
}

// A partial batch must still flush once Linger elapses.
func TestNATS_FlushesPartialBatchAfterLinger(t *testing.T) {
	b := natsBus(t)
	topic := eventbus.NewTopic[int](uniqueName("telemetry.test_linger_"))

	batches := make(chan []int, 4)
	eventbus.SubscribeBatch(b, topic, uniqueName("g_"), func(_ context.Context, payloads []int) error {
		batches <- payloads
		return nil
	}, eventbus.WithBatchSize(100), eventbus.WithLinger(time.Second))

	if err := eventbus.Publish(context.Background(), b, topic, 42); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case got := <-batches:
		if len(got) != 1 || got[0] != 42 {
			t.Fatalf("batch = %v, want [42]", got)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("partial batch never flushed")
	}
}

// A handler error nacks the batch, so JetStream redelivers it — this is what
// keeps telemetry from being lost when the database is briefly unavailable.
func TestNATS_RedeliversAfterHandlerError(t *testing.T) {
	b := natsBus(t)
	topic := eventbus.NewTopic[int](uniqueName("telemetry.test_redeliver_"))

	var attempts atomic.Int64
	done := make(chan struct{})
	eventbus.SubscribeBatch(b, topic, uniqueName("g_"), func(_ context.Context, _ []int) error {
		if attempts.Add(1) == 1 {
			return context.DeadlineExceeded // pretend the insert failed
		}
		close(done)
		return nil
	}, eventbus.WithBatchSize(1), eventbus.WithLinger(time.Second))

	if err := eventbus.Publish(context.Background(), b, topic, 7); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case <-done:
		if n := attempts.Load(); n < 2 {
			t.Fatalf("attempts = %d, want at least 2", n)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("payload never redelivered (attempts=%d)", attempts.Load())
	}
}
