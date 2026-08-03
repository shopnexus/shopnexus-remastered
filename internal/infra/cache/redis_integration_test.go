//go:build integration

package cache_test

import (
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/redis/rueidis"
	"shopnexus/internal/infra/cache"
)

// newTestCache connects to REDIS_ADDR, skipping when it is unset — the same contract
// every integration test here follows.
//
// REDIS_PASSWORD is read too, because the dev container runs with `--requirepass` and a
// client that omits it fails the handshake with NOAUTH rather than skipping. Same pair of
// variables cmd/gateway builds its client from.
func newTestCache(t *testing.T) cache.Client {
	t.Helper()
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		t.Skip("REDIS_ADDR not set")
	}
	rdb, err := rueidis.NewClient(rueidis.ClientOption{
		InitAddress: []string{addr},
		Password:    os.Getenv("REDIS_PASSWORD"),
	})
	if err != nil {
		t.Fatalf("connect redis: %v", err)
	}
	c := cache.NewRedisClient(rdb)
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Errorf("close cache: %v", err)
		}
	})
	return c
}

func TestGetDelReadsThenRemoves(t *testing.T) {
	c := newTestCache(t)
	key := "getdel-" + t.Name()

	if err := c.Set(t.Context(), key, "hello", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	var got string
	if err := c.GetDel(t.Context(), key, &got); err != nil {
		t.Fatalf("GetDel: %v", err)
	}
	if got != "hello" {
		t.Errorf("got %q, want hello", got)
	}

	var again string
	if err := c.GetDel(t.Context(), key, &again); !errors.Is(err, cache.ErrCacheMiss) {
		t.Fatalf("second GetDel err = %v, want ErrCacheMiss", err)
	}
}

func TestGetDelMissingKey(t *testing.T) {
	c := newTestCache(t)

	var got string
	err := c.GetDel(t.Context(), "getdel-absent-"+t.Name(), &got)
	if !errors.Is(err, cache.ErrCacheMiss) {
		t.Fatalf("err = %v, want ErrCacheMiss", err)
	}
}

// This is the property the whole ticket scheme rests on: concurrent redemptions of
// one key must produce exactly one winner.
func TestGetDelIsAtomic(t *testing.T) {
	c := newTestCache(t)
	key := "getdel-race-" + t.Name()

	if err := c.Set(t.Context(), key, "once", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	const racers = 16
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		wins  int
		other []error
	)
	for range racers {
		wg.Go(func() {
			var got string
			err := c.GetDel(t.Context(), key, &got)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				wins++
			case errors.Is(err, cache.ErrCacheMiss):
			default:
				other = append(other, err)
			}
		})
	}
	wg.Wait()

	if len(other) > 0 {
		t.Fatalf("unexpected errors: %v", other)
	}
	if wins != 1 {
		t.Fatalf("%d racers read the value, want exactly 1", wins)
	}
}
