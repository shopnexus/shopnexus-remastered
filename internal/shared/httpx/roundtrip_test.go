package httpx_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"shopnexus/internal/shared/httpx"
)

// recorder collects observed calls; the transport reports on the caller's
// goroutine, but a stream test reads from another one.
type recorder struct {
	mu    sync.Mutex
	calls []httpx.OutboundCall
}

func (r *recorder) observe(_ context.Context, call httpx.OutboundCall) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
}

func (r *recorder) all() []httpx.OutboundCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]httpx.OutboundCall(nil), r.calls...)
}

func clientFor(provider string, obs httpx.OutboundObserver) *http.Client {
	return &http.Client{Transport: httpx.ObserveOutbound(provider, nil, obs)}
}

func TestObserveOutbound_RecordsCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	var rec recorder
	// A query string must not reach the observer: it can carry credentials.
	resp, err := clientFor("litellm", rec.observe).Post(srv.URL+"/v1/chat/completions?api_key=secret", "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if string(body) != `{"ok":true}` {
		t.Errorf("body = %q: the transport must pass the response through untouched", body)
	}
	calls := rec.all()
	if len(calls) != 1 {
		t.Fatalf("calls = %+v", calls)
	}
	call := calls[0]
	if call.Provider != "litellm" || call.Method != http.MethodPost {
		t.Errorf("call = %+v", call)
	}
	if call.Path != "/v1/chat/completions" {
		t.Errorf("path = %q, want no query string", call.Path)
	}
	if call.StatusCode != http.StatusCreated || call.Err != nil {
		t.Errorf("call = %+v", call)
	}
	if call.Duration <= 0 {
		t.Errorf("duration = %s", call.Duration)
	}
	if call.Failed() {
		t.Error("2xx must not count as a failure")
	}
}

func TestObserveOutbound_TransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL + "/v1/embeddings"
	srv.Close() // nothing is listening now

	var rec recorder
	if _, err := clientFor("litellm", rec.observe).Get(url); err == nil {
		t.Fatal("expected a dial error")
	}

	calls := rec.all()
	if len(calls) != 1 {
		t.Fatalf("calls = %+v", calls)
	}
	call := calls[0]
	if call.StatusCode != 0 {
		t.Errorf("status = %d, want 0 when no response arrived", call.StatusCode)
	}
	if call.Err == nil || !call.Failed() {
		t.Errorf("call = %+v, want a failure", call)
	}
}

// A rejected bad request is a correct answer and must not count against the
// dependency; 5xx and rate limiting must.
func TestOutboundCall_FailedByStatus(t *testing.T) {
	for _, tc := range []struct {
		status int
		failed bool
	}{
		{http.StatusOK, false},
		{http.StatusBadRequest, false},
		{http.StatusNotFound, false},
		{http.StatusUnauthorized, false},
		{http.StatusTooManyRequests, true},
		{http.StatusInternalServerError, true},
		{http.StatusBadGateway, true},
	} {
		if got := (httpx.OutboundCall{StatusCode: tc.status}).Failed(); got != tc.failed {
			t.Errorf("status %d: failed = %v, want %v", tc.status, got, tc.failed)
		}
	}
}

// For a streamed response the transport returns at the headers, so the observed
// duration is time-to-first-byte — it must not wait for the last chunk.
func TestObserveOutbound_StreamRecordsTimeToFirstByte(t *testing.T) {
	const tail = 150 * time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "first\n")
		w.(http.Flusher).Flush()
		time.Sleep(tail) // keep the stream open well past the headers
		io.WriteString(w, "last\n")
	}))
	defer srv.Close()

	var rec recorder
	resp, err := clientFor("litellm", rec.observe).Get(srv.URL + "/v1/chat/completions")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	observed := rec.all()
	if len(observed) != 1 {
		t.Fatalf("calls = %+v", observed)
	}
	if d := observed[0].Duration; d >= tail {
		t.Errorf("duration = %s, want time-to-first-byte (< %s)", d, tail)
	}

	body, err := io.ReadAll(resp.Body) // the body must still be fully readable
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "first\nlast\n" {
		t.Errorf("body = %q", body)
	}
}

func TestObserveOutbound_NilObserverIsPassThrough(t *testing.T) {
	base := http.DefaultTransport
	if got := httpx.ObserveOutbound("litellm", base, nil); got != base {
		t.Error("a nil observer must return the transport unwrapped")
	}
	if httpx.ObserveOutbound("litellm", nil, nil) != http.DefaultTransport {
		t.Error("a nil transport must fall back to http.DefaultTransport")
	}
}

func TestLogOutbound_DoesNotPanic(t *testing.T) {
	// Behaviour here is a log line; the contract worth testing is that the
	// observer tolerates a call with no response and no error text.
	// Debug level so both the failure and the success branch actually render.
	log := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
	obs := httpx.LogOutbound(log)
	obs(context.Background(), httpx.OutboundCall{Provider: "litellm", Method: "POST", Path: "/v1/embeddings"})
	obs(context.Background(), httpx.OutboundCall{Provider: "litellm", StatusCode: 503, Err: context.DeadlineExceeded})
}
