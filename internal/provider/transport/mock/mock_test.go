package mock_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"shopnexus/internal/provider/transport"
	transportmock "shopnexus/internal/provider/transport/mock"
)

func newCourier() transport.Client {
	return transportmock.NewClient(slog.New(slog.DiscardHandler))
}

// The costs have to differ, or a per-carrier quote list is the same number under several names and
// the buyer's choice is decorative. The delays are not timed here — a test that waited on a stdlib
// timer would only be slow.
func TestQuote_PricesEachCarrierApart(t *testing.T) {
	c := newCourier()
	seen := map[int64]string{}
	for _, option := range []string{
		// The row every deployment seeds is not in the scenario table, and it has to keep
		// working: an unknown slug is priced like standard rather than refusing a checkout.
		"standard-delivery",
		transportmock.OptionStandard,
		transportmock.OptionExpress,
		transportmock.OptionEconomy,
	} {
		q, err := c.Quote(context.Background(), transport.QuoteParams{Option: option})
		if err != nil {
			t.Fatalf("Quote(%q): %v", option, err)
		}
		if q.Cost <= 0 {
			t.Fatalf("Quote(%q) = %d, want a price", option, q.Cost)
		}
		seen[q.Cost] = option
	}
	// Three distinct prices across the three named tiers; the unknown slug shares standard's.
	if len(seen) != 3 {
		t.Errorf("prices = %v, want one per tier", seen)
	}
}

// A carrier that will not price a route goes missing from the quote list rather than failing the
// page, which only works if it says so by failing.
func TestQuote_NoServiceRefuses(t *testing.T) {
	_, err := newCourier().Quote(context.Background(), transport.QuoteParams{
		Option: transportmock.OptionNoService,
	})
	if err == nil {
		t.Fatal("Quote succeeded, want this carrier to decline the route")
	}
}

func TestCreate_BooksAndStampsATrackingID(t *testing.T) {
	tr, err := newCourier().Create(context.Background(), transport.CreateParams{
		Option: transportmock.OptionExpress,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var d map[string]string
	if json.Unmarshal(tr.Data, &d) != nil || d["tracking_id"] == "" {
		t.Fatalf("expected tracking_id in data, got %s", tr.Data)
	}
	if tr.ID != d["tracking_id"] {
		t.Errorf("id = %q but data says %q; the retry guard keys on one of them", tr.ID, d["tracking_id"])
	}
	if tr.Option != transportmock.OptionExpress {
		t.Errorf("option = %q, want the carrier that was booked", tr.Option)
	}
}

// The fee is already collected by the time a booking is attempted, so a refusal is a shipment to
// retry rather than an order to reject — which is the case this scenario exists to produce.
func TestCreate_BookingFailsRefusesAfterTheQuote(t *testing.T) {
	c := newCourier()
	if _, err := c.Quote(context.Background(), transport.QuoteParams{
		Option: transportmock.OptionBookingFails,
	}); err != nil {
		t.Fatalf("Quote: %v, want this carrier to price before it refuses", err)
	}
	if _, err := c.Create(context.Background(), transport.CreateParams{
		Option: transportmock.OptionBookingFails,
	}); err == nil {
		t.Fatal("Create succeeded, want the booking refused")
	}
}

func TestWireWebhooks_ManualTrigger(t *testing.T) {
	var mu sync.Mutex
	var got transport.WebhookResult
	mux := http.NewServeMux()
	key := newCourier().WireWebhooks(mux, func(_ context.Context, r transport.WebhookResult) error {
		mu.Lock()
		defer mu.Unlock()
		got = r
		return nil
	})
	// The key is the path itself, and it is under the prefix the router mounts this mux at —
	// a route registered anywhere else is a webhook no carrier can ever reach.
	if key != "/webhooks/transport/mock" {
		t.Fatalf("key = %q", key)
	}

	req := httptest.NewRequest(http.MethodPost, key,
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
