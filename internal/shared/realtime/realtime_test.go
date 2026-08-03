package realtime_test

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"shopnexus/internal/shared/realtime"
)

type payload struct {
	ID   string `json:"id"`
	Body string `json:"body"`
}

var thingHappened = realtime.NewEvent[payload]("test.thing_happened")

// fakeFanout records what was published, so a service test never needs a bus.
type fakeFanout struct {
	mu   sync.Mutex
	sent []sentMessage
	err  error
}

type sentMessage struct {
	subject string
	payload []byte
}

func (f *fakeFanout) Broadcast(subject string, b []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, sentMessage{subject: subject, payload: b})
	return nil
}

func (f *fakeFanout) OnBroadcast(string, func([]byte)) (func(), error) {
	return func() {}, nil
}

func TestNotifyBuildsTheEnvelope(t *testing.T) {
	f := &fakeFanout{}
	before := time.Now()

	err := realtime.Notify(t.Context(), f, 42, thingHappened, payload{ID: "x", Body: "hi"})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}

	if len(f.sent) != 1 {
		t.Fatalf("published %d messages, want 1", len(f.sent))
	}
	if got, want := f.sent[0].subject, realtime.AccountSubject(42); got != want {
		t.Errorf("subject = %q, want %q", got, want)
	}

	var env struct {
		Code string    `json:"code"`
		At   time.Time `json:"at"`
		Data payload   `json:"data"`
	}
	if err := json.Unmarshal(f.sent[0].payload, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Code != "test.thing_happened" {
		t.Errorf("code = %q", env.Code)
	}
	if env.At.Before(before) {
		t.Errorf("at = %v, want at or after %v", env.At, before)
	}
	if env.Data.Body != "hi" {
		t.Errorf("data.body = %q, want hi", env.Data.Body)
	}
}

// One socket carries exactly one account's events, so the subject is the whole
// authorisation and it must not collide across accounts.
func TestAccountSubjectIsPerAccount(t *testing.T) {
	if realtime.AccountSubject(1) == realtime.AccountSubject(2) {
		t.Fatal("two accounts share a subject")
	}
	if got := realtime.AccountSubject(42); got != "ws.acct.42" {
		t.Errorf("AccountSubject(42) = %q, want ws.acct.42", got)
	}
}

func TestNotifyRejectsAnUnaddressedEvent(t *testing.T) {
	f := &fakeFanout{}

	err := realtime.Notify(t.Context(), f, 0, thingHappened, payload{ID: "x"})
	if err == nil {
		t.Fatal("Notify succeeded with accountID 0; there is no such recipient")
	}
	if len(f.sent) != 0 {
		t.Errorf("published %d messages, want 0", len(f.sent))
	}
}

func TestNotifyWrapsATransportFailure(t *testing.T) {
	sentinel := errors.New("bus down")
	f := &fakeFanout{err: sentinel}

	err := realtime.Notify(t.Context(), f, 42, thingHappened, payload{ID: "x"})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want it to wrap %v", err, sentinel)
	}
}

func TestDecodeEnvelope(t *testing.T) {
	f := &fakeFanout{}
	if err := realtime.Notify(t.Context(), f, 42, thingHappened, payload{ID: "x", Body: "hi"}); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	env, err := realtime.DecodeEnvelope(f.sent[0].payload)
	if err != nil {
		t.Fatalf("DecodeEnvelope: %v", err)
	}
	if env.Code != "test.thing_happened" {
		t.Errorf("code = %q", env.Code)
	}

	got, ok := realtime.DataOf(env, thingHappened)
	if !ok {
		t.Fatal("DataOf reported the wrong event")
	}
	if got.Body != "hi" {
		t.Errorf("body = %q, want hi", got.Body)
	}

	other := realtime.NewEvent[payload]("test.something_else")
	if _, ok := realtime.DataOf(env, other); ok {
		t.Error("DataOf matched a different event's code")
	}
}
