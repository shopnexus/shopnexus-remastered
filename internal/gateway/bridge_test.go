package gateway_test

import (
	"encoding/json/v2"
	"log/slog"
	"sync"
	"testing"
	"time"

	"shopnexus/internal/gateway"
	"shopnexus/internal/infra/eventbus"
	"shopnexus/internal/module/order"
	"shopnexus/internal/shared/realtime"
)

type bridgeSpy struct {
	mu   sync.Mutex
	sent map[string][]string // subject → codes
	done chan struct{}
}

func newBridgeSpy() *bridgeSpy {
	return &bridgeSpy{sent: map[string][]string{}, done: make(chan struct{}, 8)}
}

func (b *bridgeSpy) Broadcast(subject string, payload []byte) error {
	var env realtime.Envelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return err
	}
	b.mu.Lock()
	b.sent[subject] = append(b.sent[subject], env.Code)
	b.mu.Unlock()
	b.done <- struct{}{}
	return nil
}

func (b *bridgeSpy) OnBroadcast(string, func([]byte)) (func(), error) { return func() {}, nil }

func (b *bridgeSpy) wait(t *testing.T, n int) {
	t.Helper()
	for range n {
		select {
		case <-b.done:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for %d broadcasts", n)
		}
	}
}

// Both sides of a sale watch it, so one Redis event becomes two pushes.
func TestBridgeOrderPlacedReachesBuyerAndSeller(t *testing.T) {
	bus := eventbus.NewMemory(slog.New(slog.DiscardHandler))
	t.Cleanup(func() {
		if err := bus.Close(); err != nil {
			t.Errorf("close bus: %v", err)
		}
	})

	spy := newBridgeSpy()
	gateway.BridgeOrderEvents(bus, spy, slog.New(slog.DiscardHandler))

	err := eventbus.Publish(t.Context(), bus, order.OrderPlacedTopic, order.OrderPlaced{
		OrderID:  9,
		BuyerID:  42,
		SellerID: 77,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	spy.wait(t, 2)

	spy.mu.Lock()
	defer spy.mu.Unlock()
	for _, accountID := range []int64{42, 77} {
		subject := realtime.AccountSubject(accountID)
		codes := spy.sent[subject]
		if len(codes) != 1 || codes[0] != "order.placed" {
			t.Errorf("subject %s got %v, want [order.placed]", subject, codes)
		}
	}
}

func TestBridgeOrderSettled(t *testing.T) {
	bus := eventbus.NewMemory(slog.New(slog.DiscardHandler))
	t.Cleanup(func() {
		if err := bus.Close(); err != nil {
			t.Errorf("close bus: %v", err)
		}
	})

	spy := newBridgeSpy()
	gateway.BridgeOrderEvents(bus, spy, slog.New(slog.DiscardHandler))

	err := eventbus.Publish(t.Context(), bus, order.OrderSettledTopic, order.OrderSettled{
		OrderID:  9,
		BuyerID:  42,
		SellerID: 77,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	spy.wait(t, 2)

	spy.mu.Lock()
	defer spy.mu.Unlock()
	if codes := spy.sent[realtime.AccountSubject(42)]; len(codes) != 1 || codes[0] != "order.settled" {
		t.Errorf("buyer got %v, want [order.settled]", codes)
	}
}
