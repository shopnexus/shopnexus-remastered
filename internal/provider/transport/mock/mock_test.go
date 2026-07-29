package mock_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"shopnexus/internal/provider"
	"shopnexus/internal/provider/transport"
	transportmock "shopnexus/internal/provider/transport/mock"
)

func TestQuoteAndCreate(t *testing.T) {
	c := transportmock.NewClient(provider.Option{Provider: "mock"})

	q, err := c.Quote(context.Background(), transport.QuoteParams{})
	if err != nil || q.Cost <= 0 {
		t.Fatalf("quote: %+v err=%v", q, err)
	}

	tr, err := c.Create(context.Background(), transport.CreateParams{Option: "standard"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var d map[string]string
	if json.Unmarshal(tr.Data, &d) != nil || d["tracking_id"] == "" {
		t.Fatalf("expected tracking_id in data, got %s", tr.Data)
	}
}

func TestWireWebhooks_ManualTrigger(t *testing.T) {
	c := transportmock.NewClient(provider.Option{Provider: "mock"})

	var mu sync.Mutex
	var got transport.WebhookResult
	mux := http.NewServeMux()
	key := c.WireWebhooks(mux, func(_ context.Context, r transport.WebhookResult) error {
		mu.Lock()
		defer mu.Unlock()
		got = r
		return nil
	}, map[string]struct{}{})
	if key != "transport/mock" {
		t.Fatalf("key = %q", key)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/transport/webhook/mock",
		strings.NewReader(`{"tracking_id":"MOCKAB12","status":"Delivered"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	mu.Lock()
	defer mu.Unlock()
	if got.TransportID != "MOCKAB12" || got.Status != "Delivered" {
		t.Fatalf("unexpected webhook result: %+v", got)
	}
}

// WireWebhooks is idempotent: a second call with the key already registered
// is a no-op that returns the key without re-mounting.
func TestWireWebhooks_Idempotent(t *testing.T) {
	c := transportmock.NewClient(provider.Option{Provider: "mock"})
	mux := http.NewServeMux()
	registered := map[string]struct{}{"transport/mock": {}}
	if key := c.WireWebhooks(mux, func(context.Context, transport.WebhookResult) error { return nil }, registered); key != "transport/mock" {
		t.Fatalf("key = %q", key)
	}
}
