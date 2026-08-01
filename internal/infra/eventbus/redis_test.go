//go:build integration

package eventbus_test

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/rueidis"

	"shopnexus/internal/infra/eventbus"
)

// DB 15 keeps test keys out of dev data. The address and the password come from the environment,
// like every other adapter test's DSN: a credential in a source file is one that ships.
const testRedisDB = 15

func redisEnv(t *testing.T) (addr, password string) {
	t.Helper()
	addr = os.Getenv("REDIS_ADDR")
	if addr == "" {
		t.Skip("REDIS_ADDR not set")
	}
	return addr, os.Getenv("REDIS_PASSWORD")
}

// newTestRedis returns a client on a fresh uuid topic. Cleanup deletes only
// this test's stream (tests share DB 15 in parallel — no FLUSHDB).
func newTestRedis(t *testing.T) (eventbus.Client, eventbus.Topic[userEvent]) {
	t.Helper()
	addr, password := redisEnv(t)
	rdb, err := rueidis.NewClient(rueidis.ClientOption{
		InitAddress: []string{addr},
		Password:    password,
		SelectDB:    testRedisDB,
	})
	if err != nil {
		t.Skipf("redis unavailable: %v", err)
	}
	if pingErr := rdb.Do(context.Background(), rdb.B().Ping().Build()).Error(); pingErr != nil {
		rdb.Close()
		t.Skipf("redis unavailable: %v", pingErr)
	}
	topic := eventbus.NewTopic[userEvent]("test." + uuid.NewString())
	tr := eventbus.NewRedis(rdb, nil)
	t.Cleanup(func() {
		tr.Close()
		rdb.Do(context.Background(), rdb.B().Del().Key("bus:"+topic.Name).Build())
		rdb.Close()
	})
	return tr, topic
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met within deadline")
}

func TestRedisRoundtrip(t *testing.T) {
	t.Parallel()
	c, topic := newTestRedis(t)

	var mu sync.Mutex
	var got []userEvent
	eventbus.Subscribe(c, topic, "g1", func(_ context.Context, p userEvent) error {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, p)
		return nil
	})
	time.Sleep(300 * time.Millisecond) // let the consumer create the group before publishing

	want := userEvent{ID: 7, Name: "redis"}
	if err := eventbus.Publish(context.Background(), c, topic, want); err != nil {
		t.Fatal(err)
	}

	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) == 1
	})
	mu.Lock()
	defer mu.Unlock()
	if got[0] != want {
		t.Fatalf("got %+v, want %+v", got[0], want)
	}
}

func TestRedisBatchDelivery(t *testing.T) {
	t.Parallel()
	c, topic := newTestRedis(t)

	var mu sync.Mutex
	total := 0
	batches := 0
	eventbus.SubscribeBatch(c, topic, "g1", func(_ context.Context, payloads []userEvent) error {
		mu.Lock()
		defer mu.Unlock()
		total += len(payloads)
		batches++
		return nil
	}, eventbus.WithBatchSize(10), eventbus.WithLinger(time.Second))
	time.Sleep(300 * time.Millisecond)

	const n = 10
	for i := range n {
		if err := eventbus.Publish(context.Background(), c, topic, userEvent{ID: i, Name: "b"}); err != nil {
			t.Fatal(err)
		}
	}

	// XREADGROUP returns as soon as entries are available, so expect fewer
	// batches than payloads but no fixed batch shape.
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return total == n
	})
	mu.Lock()
	defer mu.Unlock()
	if batches < 1 || batches > n {
		t.Fatalf("batches = %d", batches)
	}
	t.Logf("delivered %d payloads in %d batches", total, batches)
}

func TestRedisGroupsFanOutConsumersCompete(t *testing.T) {
	t.Parallel()
	c, topic := newTestRedis(t)

	var mu sync.Mutex
	counts := map[string]int{}
	add := func(key string) func(context.Context, userEvent) error {
		return func(_ context.Context, _ userEvent) error {
			mu.Lock()
			defer mu.Unlock()
			counts[key]++
			return nil
		}
	}
	eventbus.Subscribe(c, topic, "g1", add("g1-a"))
	eventbus.Subscribe(c, topic, "g1", add("g1-b"))
	eventbus.Subscribe(c, topic, "g2", add("g2"))
	time.Sleep(300 * time.Millisecond)

	const n = 6
	for i := range n {
		if err := eventbus.Publish(context.Background(), c, topic, userEvent{ID: i, Name: "f"}); err != nil {
			t.Fatal(err)
		}
	}

	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return counts["g1-a"]+counts["g1-b"] == n && counts["g2"] == n
	})
}
