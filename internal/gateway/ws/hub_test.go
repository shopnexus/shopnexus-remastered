package ws_test

import (
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"shopnexus/internal/gateway/ws"
	"shopnexus/internal/shared/realtime"
)

// fakeFanout lets a test deliver to a subject and observe subscribe/unsubscribe.
type fakeFanout struct {
	mu      sync.Mutex
	handler map[string]func([]byte)
	subs    map[string]int // net subscriptions per subject
	err     error
	// beforeSubscribe runs before OnBroadcast takes the lock, so a test can hold a Join
	// inside the subscribe window and race a second one against it.
	beforeSubscribe func()
}

func newFakeFanout() *fakeFanout {
	return &fakeFanout{handler: map[string]func([]byte){}, subs: map[string]int{}}
}

func (f *fakeFanout) Broadcast(string, []byte) error { return nil }

func (f *fakeFanout) OnBroadcast(subject string, h func([]byte)) (func(), error) {
	if f.beforeSubscribe != nil {
		f.beforeSubscribe()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	f.handler[subject] = h
	f.subs[subject]++
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.subs[subject]--
		delete(f.handler, subject)
	}, nil
}

// deliver simulates NATS pushing a message on subject.
func (f *fakeFanout) deliver(t *testing.T, subject string, b []byte) {
	t.Helper()
	f.mu.Lock()
	h := f.handler[subject]
	f.mu.Unlock()
	if h == nil {
		t.Fatalf("nothing subscribed to %s", subject)
	}
	h(b)
}

func (f *fakeFanout) subCount(subject string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.subs[subject]
}

func testConfig() ws.Config {
	return ws.Config{SendBuffer: 4, MaxPerAccount: 3}
}

func newHub(f realtime.Fanout) *ws.Hub {
	return ws.NewHub(f, slog.New(slog.DiscardHandler), testConfig())
}

func TestJoinDeliversToEverySocketOfTheAccount(t *testing.T) {
	f := newFakeFanout()
	hub := newHub(f)

	first, err := hub.Join(42)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	defer hub.Leave(first)

	second, err := hub.Join(42)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	defer hub.Leave(second)

	f.deliver(t, realtime.AccountSubject(42), []byte("event"))

	for i, c := range []*ws.Client{first, second} {
		select {
		case got := <-c.Out():
			if string(got) != "event" {
				t.Errorf("socket %d got %q", i, got)
			}
		case <-time.After(time.Second):
			t.Errorf("socket %d received nothing", i)
		}
	}
}

// One subscription per account, not per socket: three tabs must not triple the traffic.
func TestJoinSubscribesOncePerAccount(t *testing.T) {
	f := newFakeFanout()
	hub := newHub(f)
	subject := realtime.AccountSubject(42)

	first, err := hub.Join(42)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if got := f.subCount(subject); got != 1 {
		t.Fatalf("subscriptions = %d, want 1", got)
	}

	second, err := hub.Join(42)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if got := f.subCount(subject); got != 1 {
		t.Fatalf("subscriptions after second join = %d, want 1", got)
	}

	// The subject survives while any socket remains.
	hub.Leave(first)
	if got := f.subCount(subject); got != 1 {
		t.Fatalf("subscriptions after one leave = %d, want 1", got)
	}

	// The last one out cancels it, or a replica keeps paying for an account it no
	// longer serves.
	hub.Leave(second)
	if got := f.subCount(subject); got != 0 {
		t.Fatalf("subscriptions after last leave = %d, want 0", got)
	}
}

func TestLeaveIsIdempotent(t *testing.T) {
	f := newFakeFanout()
	hub := newHub(f)

	c, err := hub.Join(42)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	hub.Leave(c)
	hub.Leave(c) // a write pump and a read loop can both notice a dead socket

	if got := f.subCount(realtime.AccountSubject(42)); got != 0 {
		t.Errorf("subscriptions = %d, want 0", got)
	}
	if got := hub.Count(); got != 0 {
		t.Errorf("Count = %d, want 0", got)
	}
}

// A slow consumer is dropped, never waited for: the handler runs on the NATS dispatch
// goroutine, so blocking there stalls every subject on the connection.
func TestSlowConsumerIsClosedNotBlocking(t *testing.T) {
	f := newFakeFanout()
	hub := newHub(f)
	subject := realtime.AccountSubject(42)

	c, err := hub.Join(42)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}

	// SendBuffer is 4 and nothing is reading, so the fifth delivery overflows.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 5 {
			f.deliver(t, subject, []byte("event"))
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("delivery blocked on a full buffer")
	}

	select {
	case <-c.Done():
	case <-time.After(time.Second):
		t.Fatal("overflowing socket was not closed")
	}

	if got := f.subCount(subject); got != 0 {
		t.Errorf("subscriptions = %d, want 0 — dropping the last socket unsubscribes", got)
	}
}

func TestJoinRefusesTooManySockets(t *testing.T) {
	f := newFakeFanout()
	hub := newHub(f) // MaxPerAccount is 3

	for i := range 3 {
		if _, err := hub.Join(42); err != nil {
			t.Fatalf("Join %d: %v", i, err)
		}
	}

	if _, err := hub.Join(42); !errors.Is(err, ws.ErrTooManySockets) {
		t.Fatalf("err = %v, want ErrTooManySockets", err)
	}

	// A different account is unaffected: the cap is per account, not global.
	if _, err := hub.Join(43); err != nil {
		t.Fatalf("Join for another account: %v", err)
	}
}

func TestJoinSurfacesASubscribeFailure(t *testing.T) {
	f := newFakeFanout()
	f.err = errors.New("nats down")
	hub := newHub(f)

	if _, err := hub.Join(42); err == nil {
		t.Fatal("Join succeeded while the bus was refusing subscriptions")
	}
	if got := hub.Count(); got != 0 {
		t.Errorf("Count = %d, want 0 — a failed join must leave nothing behind", got)
	}
}

// A socket that attached behind a first joiner whose subscription then failed must be
// closed, not left holding a connection that receives nothing for ever.
func TestFailedSubscribeStrandsNobody(t *testing.T) {
	f := newFakeFanout()
	// blockSubscribe lets the test hold the first Join inside OnBroadcast, which is the
	// window a second Join slips through — it sees the slot claimed and attaches.
	release := make(chan struct{})
	f.beforeSubscribe = func() { <-release }
	f.err = errors.New("nats down")
	hub := newHub(f)

	type result struct {
		client *ws.Client
		err    error
	}
	firstDone := make(chan result, 1)
	go func() {
		c, err := hub.Join(42)
		firstDone <- result{client: c, err: err}
	}()

	// Wait until the first Join has claimed the slot and is inside OnBroadcast.
	var second *ws.Client
	for range 100 {
		time.Sleep(5 * time.Millisecond)
		c, err := hub.Join(42)
		if err == nil && c != nil {
			second = c
			break
		}
	}
	if second == nil {
		t.Fatal("could not attach a second socket while the first was subscribing")
	}

	close(release)

	got := <-firstDone
	if got.err == nil {
		t.Fatal("first Join succeeded while the bus was refusing subscriptions")
	}

	select {
	case <-second.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("the second socket was stranded: subscription failed but it stayed open")
	}
	if n := hub.Count(); n != 0 {
		t.Errorf("Count = %d, want 0", n)
	}
}

// Events for one account never reach another's socket.
func TestDeliveryIsIsolatedPerAccount(t *testing.T) {
	f := newFakeFanout()
	hub := newHub(f)

	mine, err := hub.Join(42)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	defer hub.Leave(mine)

	theirs, err := hub.Join(43)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	defer hub.Leave(theirs)

	f.deliver(t, realtime.AccountSubject(43), []byte("theirs"))

	select {
	case got := <-mine.Out():
		t.Fatalf("account 42 received %q", got)
	case <-time.After(200 * time.Millisecond):
	}
	select {
	case got := <-theirs.Out():
		if string(got) != "theirs" {
			t.Errorf("got %q", got)
		}
	case <-time.After(time.Second):
		t.Error("account 43 received nothing")
	}
}
