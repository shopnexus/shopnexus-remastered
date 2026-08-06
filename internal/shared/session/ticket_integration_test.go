//go:build integration

package session_test

import (
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"shopnexus/internal/infra/cache"
	"shopnexus/internal/shared/session"

	"github.com/redis/rueidis"
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

func TestTicketRoundTrip(t *testing.T) {
	tickets := session.NewTickets(newTestCache(t), 30*time.Second)

	tok, err := tickets.Issue(t.Context(), 42, "sess-abc")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if len(tok) < 20 {
		t.Fatalf("ticket %q is too short to be unguessable", tok)
	}

	accountID, sessionID, err := tickets.Redeem(t.Context(), tok)
	if err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	if accountID != 42 {
		t.Errorf("accountID = %d, want 42", accountID)
	}
	if sessionID != "sess-abc" {
		t.Errorf("sessionID = %q, want sess-abc", sessionID)
	}
}

// The property that makes a ticket safe in a URL: seeing it in a log is worthless
// once the real client has connected.
func TestTicketIsSingleUse(t *testing.T) {
	tickets := session.NewTickets(newTestCache(t), 30*time.Second)

	tok, err := tickets.Issue(t.Context(), 42, "sess-abc")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, _, err := tickets.Redeem(t.Context(), tok); err != nil {
		t.Fatalf("first Redeem: %v", err)
	}

	_, _, err = tickets.Redeem(t.Context(), tok)
	if !errors.Is(err, session.ErrInvalidTicket) {
		t.Fatalf("second Redeem err = %v, want ErrInvalidTicket", err)
	}
}

func TestTicketConcurrentRedeemHasOneWinner(t *testing.T) {
	tickets := session.NewTickets(newTestCache(t), 30*time.Second)

	tok, err := tickets.Issue(t.Context(), 42, "sess-abc")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		wins int
	)
	for range 16 {
		wg.Go(func() {
			if _, _, err := tickets.Redeem(t.Context(), tok); err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	if wins != 1 {
		t.Fatalf("%d redemptions succeeded, want 1", wins)
	}
}

func TestRedeemUnknownTicket(t *testing.T) {
	tickets := session.NewTickets(newTestCache(t), 30*time.Second)

	_, _, err := tickets.Redeem(t.Context(), "wst_deadbeef")
	if !errors.Is(err, session.ErrInvalidTicket) {
		t.Fatalf("err = %v, want ErrInvalidTicket", err)
	}
}

func TestRedeemEmptyTicket(t *testing.T) {
	tickets := session.NewTickets(newTestCache(t), 30*time.Second)

	_, _, err := tickets.Redeem(t.Context(), "")
	if !errors.Is(err, session.ErrInvalidTicket) {
		t.Fatalf("err = %v, want ErrInvalidTicket", err)
	}
}

func TestTicketExpires(t *testing.T) {
	tickets := session.NewTickets(newTestCache(t), time.Second)

	tok, err := tickets.Issue(t.Context(), 42, "sess-abc")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	time.Sleep(1500 * time.Millisecond)

	_, _, err = tickets.Redeem(t.Context(), tok)
	if !errors.Is(err, session.ErrInvalidTicket) {
		t.Fatalf("err = %v, want ErrInvalidTicket after the TTL", err)
	}
}
