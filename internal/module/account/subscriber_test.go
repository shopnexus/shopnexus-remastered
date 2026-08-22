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
	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/module/catalog"
	"shopnexus/internal/module/finance"
	financedomain "shopnexus/internal/module/finance/domain"
	"shopnexus/internal/module/order"
)

// What the subscribers are for: another module's fact becomes one notification per interested
// party, told as a *kind*. Nothing here asserts on words — the kind decides those, and the
// copybook writes them in the reader's language. What is worth asserting is which side gets
// which kind, because that is where a buyer used to be told they had an order to pack.

// bus is a memory bus wired to every subscriber, torn down with the test.
func subscribedBus(t *testing.T) (*eventbus.Memory, *spyNotifier) {
	t.Helper()
	bus := eventbus.NewMemory(slog.New(slog.DiscardHandler))
	t.Cleanup(func() {
		if err := bus.Close(); err != nil {
			t.Errorf("close bus: %v", err)
		}
	})
	spy := &spyNotifier{done: make(chan struct{}, 8)}
	log := slog.New(slog.DiscardHandler)
	account.SubscribeOrderEvents(bus, spy, log)
	account.SubscribeCatalogEvents(bus, spy, log)
	account.SubscribeFinanceEvents(bus, spy, log)
	return bus, spy
}

// kindsByAccount is what most of these tests actually check.
func kindsByAccount(reqs []accountapi.CreateNotificationRequest) map[int64]string {
	out := make(map[int64]string, len(reqs))
	for _, req := range reqs {
		out[req.AccountID.Int64()] = req.Kind
	}
	return out
}

// The two sides of a sale read different words about it — one has an order confirmed, the other
// has one to pack — so the fact reaches them as two kinds.
func TestSubscribeOrderEventsNotifiesBothSides(t *testing.T) {
	bus, spy := subscribedBus(t)

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
	byAccount := kindsByAccount(got)
	if byAccount[42] != string(domain.KindOrderPlaced) {
		t.Errorf("buyer kind = %q, want %q", byAccount[42], domain.KindOrderPlaced)
	}
	if byAccount[77] != string(domain.KindSaleReceived) {
		t.Errorf("seller kind = %q, want %q", byAccount[77], domain.KindSaleReceived)
	}

	// Both the feed copy and the mail render this map, so the terms have to be in it: a total
	// that came from somewhere else could disagree with the row beside it.
	if got[0].Payload["order_id"] == "" || got[0].Payload["total"] != int64(1250000) {
		t.Errorf("payload = %v, want the order's terms", got[0].Payload)
	}
}

// A delivered parcel is the buyer's cue to confirm receipt, which is what releases the escrow —
// so their kind is the one with a letter. The seller shipped it and gets a feed row.
func TestSubscribeOrderEventsMailsOnlyTheBuyerOnADelivery(t *testing.T) {
	bus, spy := subscribedBus(t)

	err := eventbus.Publish(t.Context(), bus, order.OrderDeliveredTopic, order.OrderDelivered{
		OrderID: 9, BuyerID: 42, SellerID: 77,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	byAccount := kindsByAccount(spy.wait(t, 2))
	assertMailed(t, byAccount[42], domain.KindOrderDelivered, true)
	assertMailed(t, byAccount[77], domain.KindSaleHandedOver, false)
}

// The seller is the one who did not act on a lapsed confirmation: chasing them is support's job
// rather than a letter this platform sends on their behalf.
func TestSubscribeOrderEventsMailsOnlyTheBuyerOnALapsedConfirmation(t *testing.T) {
	bus, spy := subscribedBus(t)

	err := eventbus.Publish(t.Context(), bus, order.OrderConfirmationLapsedTopic, order.OrderConfirmationLapsed{
		OrderID: 9, BuyerID: 42, SellerID: 77,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	byAccount := kindsByAccount(spy.wait(t, 2))
	assertMailed(t, byAccount[42], domain.KindOrderUnconfirmed, true)
	assertMailed(t, byAccount[77], domain.KindSaleUnconfirmed, false)
}

func assertMailed(t *testing.T, got string, want domain.Kind, mailed bool) {
	t.Helper()
	if got != string(want) {
		t.Errorf("kind = %q, want %q", got, want)
		return
	}
	spec, _ := domain.SpecOf(want)
	if (spec.Mail != "") != mailed {
		t.Errorf("kind %q mail = %q, want mailed=%v", want, spec.Mail, mailed)
	}
}

// A settled order is one fact with two outcomes, and the kind has to follow the outcome —
// telling both sides a cancelled sale completed is the worst version of this bug.
func TestSubscribeOrderEventsTellsCompletedFromCancelled(t *testing.T) {
	for _, tc := range []struct {
		completed   bool
		buyer, sell domain.Kind
	}{
		{completed: true, buyer: domain.KindOrderCompleted, sell: domain.KindSaleCompleted},
		{completed: false, buyer: domain.KindOrderCancelled, sell: domain.KindSaleCancelled},
	} {
		bus, spy := subscribedBus(t)
		err := eventbus.Publish(t.Context(), bus, order.OrderSettledTopic, order.OrderSettled{
			OrderID: 9, BuyerID: 42, SellerID: 77, Completed: tc.completed,
		})
		if err != nil {
			t.Fatalf("Publish: %v", err)
		}
		byAccount := kindsByAccount(spy.wait(t, 2))
		if byAccount[42] != string(tc.buyer) || byAccount[77] != string(tc.sell) {
			t.Errorf("completed=%v: kinds = %q/%q, want %q/%q",
				tc.completed, byAccount[42], byAccount[77], tc.buyer, tc.sell)
		}
	}
}

// A refund escalation exists to stop the buyer chasing a seller who stopped answering, so it is
// theirs alone — nothing goes to the party the case is against.
func TestSubscribeOrderEventsTellsOnlyTheBuyerAboutAnEscalation(t *testing.T) {
	bus, spy := subscribedBus(t)

	err := eventbus.Publish(t.Context(), bus, order.RefundEscalatedTopic, order.RefundEscalated{
		RefundID: 3, OrderID: 9, BuyerID: 42, Cause: order.EscalationUnanswered,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	got := spy.wait(t, 1)
	if len(got) != 1 || got[0].AccountID.Int64() != 42 {
		t.Fatalf("recipients = %v, want the buyer alone", kindsByAccount(got))
	}
	if got[0].Kind != string(domain.KindRefundEscalated) {
		t.Errorf("kind = %q", got[0].Kind)
	}
}

// A negotiation moving is told to whoever did not move it: the actor is looking at the thread
// they just typed in, and either side may be that actor.
func TestSubscribeOrderEventsTellsTheOtherSideOfAnOffer(t *testing.T) {
	for _, tc := range []struct {
		name     string
		actorID  int64
		wantID   int64
		change   string
		wantKind domain.Kind
	}{
		{name: "buyer countered", actorID: 42, wantID: 77, change: order.OfferChangeCountered, wantKind: domain.KindOfferCountered},
		{name: "seller accepted", actorID: 77, wantID: 42, change: order.OfferChangeAccepted, wantKind: domain.KindOfferAccepted},
		{name: "buyer withdrew", actorID: 42, wantID: 77, change: order.OfferChangeWithdrawn, wantKind: domain.KindOfferWithdrawn},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bus, spy := subscribedBus(t)
			err := eventbus.Publish(t.Context(), bus, order.OfferChangedTopic, order.OfferChanged{
				OfferID: 5, BuyerID: 42, SellerID: 77, ActorID: tc.actorID,
				Change: tc.change, ListingName: "Máy ảnh Canon", Total: 4500000, Currency: "VND",
			})
			if err != nil {
				t.Fatalf("Publish: %v", err)
			}
			got := spy.wait(t, 1)
			if got[0].AccountID.Int64() != tc.wantID {
				t.Errorf("recipient = %d, want %d — the actor already knows", got[0].AccountID.Int64(), tc.wantID)
			}
			if got[0].Kind != string(tc.wantKind) {
				t.Errorf("kind = %q, want %q", got[0].Kind, tc.wantKind)
			}
			// The copy names the listing and the price, and this module holds neither: they
			// travel on the event so no subscriber has to read back into catalog.
			if got[0].Payload["listing_name"] != "Máy ảnh Canon" || got[0].Payload["price"] != int64(4500000) {
				t.Errorf("payload = %v, want the terms on the table", got[0].Payload)
			}
		})
	}
}

// The moderator's choice is honoured here, not in catalog: whether a seller hears about a
// takedown is a product rule, and the publisher carries the flag without deciding it.
func TestSubscribeCatalogEventsHonoursNotifySeller(t *testing.T) {
	for _, tc := range []struct {
		name         string
		approved     bool
		notifySeller bool
		want         domain.Kind
	}{
		{name: "approved", approved: true, notifySeller: true, want: domain.KindListingApproved},
		{name: "taken down, told", notifySeller: true, want: domain.KindListingTakenDown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bus, spy := subscribedBus(t)
			err := eventbus.Publish(t.Context(), bus, catalog.ListingModeratedTopic, catalog.ListingModerated{
				ListingID: 11, SellerID: 77, Name: "Áo khoác", Approved: tc.approved,
				Reason: "hàng giả", NotifySeller: tc.notifySeller,
			})
			if err != nil {
				t.Fatalf("Publish: %v", err)
			}
			got := spy.wait(t, 1)
			if got[0].AccountID.Int64() != 77 || got[0].Kind != string(tc.want) {
				t.Errorf("told %d as %q, want seller 77 as %q", got[0].AccountID.Int64(), got[0].Kind, tc.want)
			}
		})
	}
}

// An approval is always told — a seller waiting on a queue is the one person who asked to be —
// but a takedown the moderator chose to keep quiet stays quiet.
func TestSubscribeCatalogEventsStaysQuietWhenAskedTo(t *testing.T) {
	bus, spy := subscribedBus(t)

	err := eventbus.Publish(t.Context(), bus, catalog.ListingModeratedTopic, catalog.ListingModerated{
		ListingID: 11, SellerID: 77, Name: "Áo khoác", Approved: false, NotifySeller: false,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	spy.wantSilence(t)
}

// A withdrawal is money the account asked to move, and the only money that leaves this platform
// on somebody's instruction — so it is worth a row. A paid buyer-checkout is not: it becomes an
// order, and `order.placed` already says so.
func TestSubscribeFinanceEventsTellsOnlyAboutMoneyLeaving(t *testing.T) {
	bus, spy := subscribedBus(t)

	err := eventbus.Publish(t.Context(), bus, finance.SessionPaidTopic, finance.SessionPaid{
		SessionID: 4, Kind: financedomain.KindBuyerCheckout, FromID: 42, Amount: 250000, Currency: "VND",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	spy.wantSilence(t)

	err = eventbus.Publish(t.Context(), bus, finance.SessionPaidTopic, finance.SessionPaid{
		SessionID: 5, Kind: financedomain.KindWithdrawal, FromID: 42, Amount: 900000, Currency: "VND",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	got := spy.wait(t, 1)
	if got[0].Kind != string(domain.KindWithdrawalPaid) || got[0].AccountID.Int64() != 42 {
		t.Errorf("told %d as %q, want 42 as %q", got[0].AccountID.Int64(), got[0].Kind, domain.KindWithdrawalPaid)
	}
	if got[0].Payload["amount"] != int64(900000) {
		t.Errorf("payload = %v, want the amount that moved", got[0].Payload)
	}
}

// A cancelled session is a claim released: the buyer's cart is still there, and a withdrawal's
// money is back in the wallet. Both are things somebody is waiting on an answer about.
func TestSubscribeFinanceEventsTellsAboutACancelledSession(t *testing.T) {
	for _, tc := range []struct {
		sessionKind string
		want        domain.Kind
	}{
		{sessionKind: financedomain.KindBuyerCheckout, want: domain.KindCheckoutExpired},
		{sessionKind: financedomain.KindWithdrawal, want: domain.KindPayoutFailed},
	} {
		bus, spy := subscribedBus(t)
		err := eventbus.Publish(t.Context(), bus, finance.SessionCancelledTopic, finance.SessionCancelled{
			SessionID: 6, Kind: tc.sessionKind, FromID: 42,
		})
		if err != nil {
			t.Fatalf("Publish: %v", err)
		}
		got := spy.wait(t, 1)
		if got[0].Kind != string(tc.want) {
			t.Errorf("session kind %q became %q, want %q", tc.sessionKind, got[0].Kind, tc.want)
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

// wantSilence asserts nothing was told. A short wait rather than an immediate read, because the
// bus delivers on its own goroutine and an empty slice read too early passes for anything.
func (s *spyNotifier) wantSilence(t *testing.T) {
	t.Helper()
	select {
	case <-s.done:
		s.mu.Lock()
		defer s.mu.Unlock()
		t.Fatalf("told somebody %v, want silence", kindsByAccount(s.got))
	case <-time.After(100 * time.Millisecond):
	}
}
