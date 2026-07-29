package eventbus_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"shopnexus/internal/infra/eventbus"
)

type userEvent struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func userTopic() eventbus.Topic[userEvent] {
	return eventbus.NewTopic[userEvent]("user.test")
}

func TestPublishSubscribeRoundtrip(t *testing.T) {
	t.Parallel()
	mem := eventbus.NewMemory(nil)
	c := eventbus.Client(mem)
	topic := userTopic()

	var mu sync.Mutex
	var got []userEvent
	eventbus.Subscribe(c, topic, "g1", func(_ context.Context, p userEvent) error {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, p)
		return nil
	})

	if err := eventbus.Publish(context.Background(), c, topic, userEvent{ID: 1, Name: "a"}); err != nil {
		t.Fatal(err)
	}
	mem.Wait()

	if len(got) != 1 || got[0] != (userEvent{ID: 1, Name: "a"}) {
		t.Fatalf("got %+v", got)
	}
}

func TestEachGroupDeliveredOnce(t *testing.T) {
	t.Parallel()
	mem := eventbus.NewMemory(nil)
	c := eventbus.Client(mem)
	topic := userTopic()

	var g1, g2 atomic.Int64
	eventbus.Subscribe(c, topic, "g1", func(_ context.Context, _ userEvent) error {
		g1.Add(1)
		return nil
	})
	eventbus.Subscribe(c, topic, "g2", func(_ context.Context, _ userEvent) error {
		g2.Add(1)
		return nil
	})

	if err := eventbus.Publish(context.Background(), c, topic, userEvent{ID: 1, Name: "a"}); err != nil {
		t.Fatal(err)
	}
	mem.Wait()

	if g1.Load() != 1 || g2.Load() != 1 {
		t.Fatalf("g1=%d g2=%d, want 1 each", g1.Load(), g2.Load())
	}
}

func TestCompetingConsumersRoundRobin(t *testing.T) {
	t.Parallel()
	mem := eventbus.NewMemory(nil)
	c := eventbus.Client(mem)
	topic := userTopic()

	var h1, h2 atomic.Int64
	eventbus.Subscribe(c, topic, "g1", func(_ context.Context, _ userEvent) error {
		h1.Add(1)
		return nil
	})
	eventbus.Subscribe(c, topic, "g1", func(_ context.Context, _ userEvent) error {
		h2.Add(1)
		return nil
	})

	for i := range 4 {
		if err := eventbus.Publish(context.Background(), c, topic, userEvent{ID: i, Name: "a"}); err != nil {
			t.Fatal(err)
		}
	}
	mem.Wait()

	if h1.Load() != 2 || h2.Load() != 2 {
		t.Fatalf("h1=%d h2=%d, want 2 each", h1.Load(), h2.Load())
	}
}

func TestPublishNoSubscribers(t *testing.T) {
	t.Parallel()
	mem := eventbus.NewMemory(nil)
	c := eventbus.Client(mem)

	if err := eventbus.Publish(context.Background(), c, userTopic(), userEvent{ID: 1, Name: "a"}); err != nil {
		t.Fatal(err)
	}
	mem.Wait()
}

func TestBatchFlushBySize(t *testing.T) {
	t.Parallel()
	mem := eventbus.NewMemory(nil)
	c := eventbus.Client(mem)
	topic := userTopic()

	var mu sync.Mutex
	var batches [][]userEvent
	eventbus.SubscribeBatch(c, topic, "g1", func(_ context.Context, payloads []userEvent) error {
		mu.Lock()
		defer mu.Unlock()
		batches = append(batches, payloads)
		return nil
	}, eventbus.WithBatchSize(3), eventbus.WithLinger(time.Minute))

	for i := range 3 {
		if err := eventbus.Publish(context.Background(), c, topic, userEvent{ID: i, Name: "a"}); err != nil {
			t.Fatal(err)
		}
	}
	mem.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(batches) != 1 || len(batches[0]) != 3 {
		t.Fatalf("batches %+v, want one batch of 3", batches)
	}
}

func TestBatchFlushByLinger(t *testing.T) {
	t.Parallel()
	mem := eventbus.NewMemory(nil)
	c := eventbus.Client(mem)
	topic := userTopic()

	var mu sync.Mutex
	var batches [][]userEvent
	eventbus.SubscribeBatch(c, topic, "g1", func(_ context.Context, payloads []userEvent) error {
		mu.Lock()
		defer mu.Unlock()
		batches = append(batches, payloads)
		return nil
	}, eventbus.WithBatchSize(100), eventbus.WithLinger(50*time.Millisecond))

	for i := range 2 {
		if err := eventbus.Publish(context.Background(), c, topic, userEvent{ID: i, Name: "a"}); err != nil {
			t.Fatal(err)
		}
	}
	mem.Wait() // returns only after linger fires and the partial batch is handled

	mu.Lock()
	defer mu.Unlock()
	if len(batches) != 1 || len(batches[0]) != 2 {
		t.Fatalf("batches %+v, want one partial batch of 2", batches)
	}
}

func TestPublishAfterClose(t *testing.T) {
	t.Parallel()
	mem := eventbus.NewMemory(nil)
	c := eventbus.Client(mem)
	topic := userTopic()

	eventbus.Subscribe(c, topic, "g1", func(_ context.Context, _ userEvent) error { return nil })
	mem.Close()

	err := eventbus.Publish(context.Background(), c, topic, userEvent{ID: 1, Name: "a"})
	if !errors.Is(err, eventbus.ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed", err)
	}
}

func TestCloseDrainsPending(t *testing.T) {
	t.Parallel()
	mem := eventbus.NewMemory(nil)
	c := eventbus.Client(mem)
	topic := userTopic()

	var handled atomic.Int64
	eventbus.SubscribeBatch(c, topic, "g1", func(_ context.Context, payloads []userEvent) error {
		handled.Add(int64(len(payloads)))
		return nil
	}, eventbus.WithBatchSize(100), eventbus.WithLinger(time.Minute))

	for i := range 5 {
		if err := eventbus.Publish(context.Background(), c, topic, userEvent{ID: i, Name: "a"}); err != nil {
			t.Fatal(err)
		}
	}
	mem.Close() // closing the queue flushes the partial batch without waiting for linger

	if handled.Load() != 5 {
		t.Fatalf("handled = %d, want 5", handled.Load())
	}
}
