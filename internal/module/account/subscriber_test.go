package account_test

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"shopnexus/internal/infra/eventbus"
	"shopnexus/internal/module/account"
	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/api/accounttest"
	"shopnexus/internal/module/order"
)

// The subscriber turns an order fact into one notification per interested party.
func TestSubscribeOrderEventsNotifiesBothSides(t *testing.T) {
	bus := eventbus.NewMemory(slog.New(slog.DiscardHandler))
	t.Cleanup(func() {
		if err := bus.Close(); err != nil {
			t.Errorf("close bus: %v", err)
		}
	})

	spy := &spyNotifier{done: make(chan struct{}, 4)}
	account.SubscribeOrderEvents(bus, spy, slog.New(slog.DiscardHandler))

	err := eventbus.Publish(t.Context(), bus, order.OrderPlacedTopic, order.OrderPlaced{
		OrderID:  9,
		BuyerID:  42,
		SellerID: 77,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	got := spy.wait(t, 2)
	if got[0].Category != "order" || got[1].Category != "order" {
		t.Errorf("categories = %q, %q, want order", got[0].Category, got[1].Category)
	}
	ids := map[int64]bool{got[0].AccountID.Int64(): true, got[1].AccountID.Int64(): true}
	if !ids[42] || !ids[77] {
		t.Errorf("recipients = %v, want buyer 42 and seller 77", ids)
	}
}

type spyNotifier struct {
	accounttest.Stub
	mu   sync.Mutex
	got  []accountapi.CreateNotificationRequest
	done chan struct{}
}

func (s *spyNotifier) CreateNotification(_ context.Context, req accountapi.CreateNotificationRequest) (accountapi.Notification, error) {
	s.mu.Lock()
	s.got = append(s.got, req)
	s.mu.Unlock()
	s.done <- struct{}{}
	return accountapi.Notification{}, nil
}

// wait blocks until n calls have landed, so the test never races the bus goroutine.
func (s *spyNotifier) wait(t *testing.T, n int) []accountapi.CreateNotificationRequest {
	t.Helper()
	for range n {
		select {
		case <-s.done:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for %d CreateNotification calls", n)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.got)
}
