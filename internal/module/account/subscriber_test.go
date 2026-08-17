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
	"shopnexus/internal/provider/notify"
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
		Total:    1250000,
		Currency: "VND",
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

	// The two sides read different words about the same sale — one has an order confirmed,
	// the other has one to pack — so the fact reaches them as two templates.
	byAccount := map[int64]string{}
	for _, req := range got {
		byAccount[req.AccountID.Int64()] = req.MailKind
	}
	if byAccount[42] != string(notify.KindOrderPlaced) {
		t.Errorf("buyer mail = %q, want %q", byAccount[42], notify.KindOrderPlaced)
	}
	if byAccount[77] != string(notify.KindOrderReceived) {
		t.Errorf("seller mail = %q, want %q", byAccount[77], notify.KindOrderReceived)
	}

	// The mail renders the payload, so the terms have to be in it: an order mail whose
	// total came from somewhere else could disagree with the feed row beside it.
	if got[0].Payload["order_id"] == "" || got[0].Payload["total"] != int64(1250000) {
		t.Errorf("payload = %v, want the order's terms", got[0].Payload)
	}
}

// A lapsed confirmation is the buyer being told their money is not stuck unnoticed. The
// seller is the one who did not act: they get the feed row, and chasing them is support's
// job rather than a letter this platform sends on their behalf.
func TestSubscribeOrderEventsMailsOnlyTheBuyerOnALapsedConfirmation(t *testing.T) {
	bus := eventbus.NewMemory(slog.New(slog.DiscardHandler))
	t.Cleanup(func() {
		if err := bus.Close(); err != nil {
			t.Errorf("close bus: %v", err)
		}
	})

	spy := &spyNotifier{done: make(chan struct{}, 4)}
	account.SubscribeOrderEvents(bus, spy, slog.New(slog.DiscardHandler))

	err := eventbus.Publish(t.Context(), bus, order.OrderConfirmationLapsedTopic, order.OrderConfirmationLapsed{
		OrderID: 9, BuyerID: 42, SellerID: 77,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	for _, req := range spy.wait(t, 2) {
		want := ""
		if req.AccountID.Int64() == 42 {
			want = string(notify.KindOrderUnconfirmed)
		}
		if req.MailKind != want {
			t.Errorf("account %d mail = %q, want %q", req.AccountID.Int64(), req.MailKind, want)
		}
	}
}

// A settled order is one fact with two outcomes, and the mail has to follow the outcome —
// telling both sides a cancelled sale completed is the worst version of this bug.
func TestSubscribeOrderEventsTellsCompletedFromCancelled(t *testing.T) {
	for _, tc := range []struct {
		completed bool
		want      notify.Kind
	}{
		{completed: true, want: notify.KindOrderCompleted},
		{completed: false, want: notify.KindOrderCancelled},
	} {
		bus := eventbus.NewMemory(slog.New(slog.DiscardHandler))
		spy := &spyNotifier{done: make(chan struct{}, 4)}
		account.SubscribeOrderEvents(bus, spy, slog.New(slog.DiscardHandler))

		err := eventbus.Publish(t.Context(), bus, order.OrderSettledTopic, order.OrderSettled{
			OrderID: 9, BuyerID: 42, SellerID: 77, Completed: tc.completed,
		})
		if err != nil {
			t.Fatalf("Publish: %v", err)
		}
		for _, req := range spy.wait(t, 2) {
			if req.MailKind != string(tc.want) {
				t.Errorf("completed=%v: mail = %q, want %q", tc.completed, req.MailKind, tc.want)
			}
		}
		if err := bus.Close(); err != nil {
			t.Errorf("close bus: %v", err)
		}
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
