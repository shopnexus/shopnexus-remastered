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
	"time"

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

// The slow courier holds the quote open, and has to give up when the caller's deadline passes rather
// than outliving the request it is answering.
func TestQuote_SlowCourierRespectsTheDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := newCourier().Quote(ctx, transport.QuoteParams{Option: transportmock.OptionSlowQuote})
	if err == nil {
		t.Fatal("Quote answered after its deadline passed")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Quote took %s, so it waited out its pause instead of the deadline", elapsed)
	}
}

// A person walks a parcel through its checkpoints, which is what the stalled scenario needs: the
// alternative was a curl command nobody runs, and a scenario nobody exercises.
func TestConsole_ReportsACheckpointAndComesBack(t *testing.T) {
	var mu sync.Mutex
	var got []transport.WebhookResult
	mux := http.NewServeMux()
	newCourier().WireWebhooks(mux, func(_ context.Context, r transport.WebhookResult) error {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, r)
		return nil
	})

	page := httptest.NewRecorder()
	mux.ServeHTTP(page, httptest.NewRequest(http.MethodGet,
		"/webhooks/transport/mock/console?tracking_id=MOCKAB12", nil))
	if page.Code != http.StatusOK {
		t.Fatalf("console status = %d, want 200", page.Code)
	}
	body := page.Body.String()
	if !strings.Contains(body, `value="MOCKAB12"`) {
		t.Error("the page does not carry the tracking id, so its form reports nothing")
	}
	// One button per checkpoint this platform models, and only those: a button for a status the
	// module ignores would look like it did something.
	for _, status := range []string{"processing", "success", "failed"} {
		if !strings.Contains(body, `value="`+status+`"`) {
			t.Errorf("the console has no %q button", status)
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhooks/transport/mock/decision",
		strings.NewReader("tracking_id=MOCKAB12&status=success"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mux.ServeHTTP(rec, req)

	// Back to the console, because a parcel is walked through several checkpoints — unlike a payment,
	// which is decided once.
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("decision status = %d, want a redirect back to the console", rec.Code)
	}
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "tracking_id=MOCKAB12") {
		t.Errorf("Location = %q, want the console for this parcel", loc)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0].TransportID != "MOCKAB12" || got[0].Status != "success" {
		t.Fatalf("reported %+v, want one delivered checkpoint", got)
	}
}

// A status the console does not offer is refused rather than reported: the form is the list of what
// this platform models, and anything else would be ignored downstream while looking accepted here.
func TestConsole_RefusesAStatusItDoesNotOffer(t *testing.T) {
	mux := http.NewServeMux()
	newCourier().WireWebhooks(mux, func(context.Context, transport.WebhookResult) error {
		t.Error("a status nobody offers was reported")
		return nil
	})

	for _, body := range []string{"tracking_id=MOCKAB12&status=held-at-customs", "status=success"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/webhooks/transport/mock/decision",
			strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("%q = %d, want 400", body, rec.Code)
		}
	}
}

// This route used to log the failure and answer 200, so a checkpoint that never landed looked exactly
// like one that did.
func TestWireWebhooks_AFailedReportSaysSo(t *testing.T) {
	mux := http.NewServeMux()
	path := newCourier().WireWebhooks(mux, func(context.Context, transport.WebhookResult) error {
		return context.DeadlineExceeded
	})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path,
		strings.NewReader(`{"tracking_id":"MOCKAB12","status":"success"}`)))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
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
