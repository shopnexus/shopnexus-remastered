//go:build integration

package eventbus_test

import (
	"testing"
	"time"

	"shopnexus/internal/infra/eventbus"
)

// newTestNATS is a local wrapper around natsBus for the fanout tests.
func newTestNATS(t *testing.T) *eventbus.NATS {
	return natsBus(t)
}

// The defining property: two subscribers to one subject both receive every message.
// JetStream's durable consumers would have split them.
func TestBroadcastReachesEverySubscriber(t *testing.T) {
	bus := newTestNATS(t)
	subject := uniqueName("ws.test.fanout.")

	first := make(chan []byte, 4)
	second := make(chan []byte, 4)

	cancelFirst, err := bus.OnBroadcast(subject, func(b []byte) { first <- b })
	if err != nil {
		t.Fatalf("OnBroadcast: %v", err)
	}
	defer cancelFirst()

	cancelSecond, err := bus.OnBroadcast(subject, func(b []byte) { second <- b })
	if err != nil {
		t.Fatalf("OnBroadcast: %v", err)
	}
	defer cancelSecond()

	if err := bus.Broadcast(subject, []byte(`{"code":"x"}`)); err != nil {
		t.Fatalf("Broadcast: %v", err)
	}

	for i, ch := range []chan []byte{first, second} {
		select {
		case got := <-ch:
			if string(got) != `{"code":"x"}` {
				t.Errorf("subscriber %d got %q", i, got)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("subscriber %d received nothing", i)
		}
	}
}

// Cancelling must actually unsubscribe, or a replica that dropped its last socket
// keeps paying for the traffic.
func TestOnBroadcastCancelStopsDelivery(t *testing.T) {
	bus := newTestNATS(t)
	subject := uniqueName("ws.test.cancel.")

	got := make(chan []byte, 4)
	cancel, err := bus.OnBroadcast(subject, func(b []byte) { got <- b })
	if err != nil {
		t.Fatalf("OnBroadcast: %v", err)
	}

	if err := bus.Broadcast(subject, []byte("first")); err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive the first message")
	}

	cancel()

	if err := bus.Broadcast(subject, []byte("second")); err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	select {
	case b := <-got:
		t.Fatalf("received %q after cancel", b)
	case <-time.After(300 * time.Millisecond):
	}
}

// Nothing persists: a message published with no listener is gone, which is why a
// reconnecting client re-reads over REST instead of expecting a replay.
func TestBroadcastWithNoSubscriberIsDropped(t *testing.T) {
	bus := newTestNATS(t)
	subject := uniqueName("ws.test.nolistener.")

	if err := bus.Broadcast(subject, []byte("into the void")); err != nil {
		t.Fatalf("Broadcast: %v", err)
	}

	got := make(chan []byte, 1)
	cancel, err := bus.OnBroadcast(subject, func(b []byte) { got <- b })
	if err != nil {
		t.Fatalf("OnBroadcast: %v", err)
	}
	defer cancel()

	select {
	case b := <-got:
		t.Fatalf("received %q; core pub/sub must not replay", b)
	case <-time.After(300 * time.Millisecond):
	}
}

// Subject filtering happens on the server, so a replica holding no socket for an
// account pays nothing for its events.
func TestBroadcastIsFilteredBySubject(t *testing.T) {
	bus := newTestNATS(t)
	mine, theirs := uniqueName("ws.test.mine."), uniqueName("ws.test.theirs.")

	got := make(chan []byte, 4)
	cancel, err := bus.OnBroadcast(mine, func(b []byte) { got <- b })
	if err != nil {
		t.Fatalf("OnBroadcast: %v", err)
	}
	defer cancel()

	if err := bus.Broadcast(theirs, []byte("not for me")); err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	select {
	case b := <-got:
		t.Fatalf("received %q from another subject", b)
	case <-time.After(300 * time.Millisecond):
	}
}
