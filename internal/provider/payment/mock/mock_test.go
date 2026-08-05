package mock_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"shopnexus/internal/provider/payment"
	paymentmock "shopnexus/internal/provider/payment/mock"
)

const base = "https://shopnexus.test"

func newRail() payment.Client {
	return paymentmock.NewClient(paymentmock.Config{BaseURL: base}, slog.New(slog.DiscardHandler))
}

// What each scenario answers on the spot. The delayed halves are not timed here — a test that
// waited eight seconds for a stdlib timer would only be slow.
func TestCharge_EachScenarioAnswersItsOwnWay(t *testing.T) {
	cases := []struct {
		option       string
		wantStatus   payment.Status
		wantRedirect bool
		wantErr      bool
	}{
		// The row every deployment seeds is not in the scenario table, and it has to keep working:
		// an unknown slug succeeds rather than failing a checkout nobody asked to break.
		{option: "platform-checkout", wantStatus: payment.StatusSuccess},
		{option: paymentmock.OptionSuccess, wantStatus: payment.StatusSuccess},
		{option: paymentmock.OptionDecline, wantStatus: payment.StatusFailed},
		{option: paymentmock.OptionRedirect, wantRedirect: true},
		// A reporting rail decides nothing on the spot: the leg has to stay pending, or the client
		// would treat the response as a receipt.
		{option: paymentmock.OptionWebhookSuccess},
		{option: paymentmock.OptionNoAnswer},
		{option: paymentmock.OptionUnreachable, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.option, func(t *testing.T) {
			res, err := newRail().Charge(context.Background(), payment.ChargeParams{
				RefID: "txn_1", Option: tc.option, Amount: 500,
			})
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Charge succeeded, want the rail to be unreachable: %+v", res)
				}
				return
			}
			if err != nil {
				t.Fatalf("Charge: %v", err)
			}
			if res.Status != tc.wantStatus {
				t.Errorf("status = %q, want %q", res.Status, tc.wantStatus)
			}
			if got := res.RedirectURL != ""; got != tc.wantRedirect {
				t.Errorf("redirect present = %v, want %v (url %q)", got, tc.wantRedirect, res.RedirectURL)
			}
			if res.ProviderID == "" {
				t.Error("no provider reference, so the leg has nothing to record")
			}
		})
	}
}

// An unreachable rail must not be reported as a decline: a failed leg reopens the session as
// "tender something else", while a rail that is down is the same rail to retry.
func TestCharge_UnreachableIsAnErrorAndNotAFailedStatus(t *testing.T) {
	res, err := newRail().Charge(context.Background(), payment.ChargeParams{
		RefID: "txn_1", Option: paymentmock.OptionUnreachable, Amount: 500,
	})
	if err == nil {
		t.Fatal("Charge succeeded")
	}
	if res.Status != "" {
		t.Errorf("status = %q, want none alongside an error", res.Status)
	}
}

// The redirect scenario is the only one a browser can walk, so the URL has to be absolute and
// reachable: a relative path is one a web client on another origin cannot follow.
func TestCharge_RedirectPointsAtThisGatewaysOwnPage(t *testing.T) {
	res, err := newRail().Charge(context.Background(), payment.ChargeParams{
		RefID: "txn_7", Option: paymentmock.OptionRedirect, Amount: 500,
		ReturnURL: "https://shopnexus.test/orders",
	})
	if err != nil {
		t.Fatalf("Charge: %v", err)
	}
	if !strings.HasPrefix(res.RedirectURL, base+"/webhooks/payment/mock/checkout?") {
		t.Fatalf("redirect = %q, want the hosted page under the configured base", res.RedirectURL)
	}
	for _, want := range []string{"ref=txn_7", "return=https%3A%2F%2Fshopnexus.test%2Forders"} {
		if !strings.Contains(res.RedirectURL, want) {
			t.Errorf("redirect %q is missing %q", res.RedirectURL, want)
		}
	}
}

// The hosted page and its form are one flow: whatever the payer presses becomes a notification,
// and then they are sent back to where the checkout started.
func TestHostedPage_DecidesTheLegAndSendsThePayerBack(t *testing.T) {
	var mu sync.Mutex
	var got []payment.Notification
	mux := http.NewServeMux()
	newRail().WireWebhooks(mux, func(_ context.Context, n payment.Notification) error {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, n)
		return nil
	})

	page := httptest.NewRecorder()
	mux.ServeHTTP(page, httptest.NewRequest(http.MethodGet,
		"/webhooks/payment/mock/checkout?ref=txn_7&amount=500", nil))
	if page.Code != http.StatusOK {
		t.Fatalf("page status = %d, want 200", page.Code)
	}
	if !strings.Contains(page.Body.String(), `value="txn_7"`) {
		t.Error("the page does not carry the reference, so its form cannot settle anything")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhooks/payment/mock/decision",
		strings.NewReader("ref=txn_7&status=failed&return=https://shopnexus.test/orders"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("decision status = %d, want a redirect back", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "https://shopnexus.test/orders" {
		t.Errorf("Location = %q, want the return URL", loc)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0].RefID != "txn_7" || got[0].Status != payment.StatusFailed {
		t.Fatalf("notifications = %+v, want one declining txn_7", got)
	}
}

// The IPN stays a route of its own: it is the only way to settle a leg the scenarios leave
// pending for ever, and the only way to move one along faster than its scenario would.
func TestWireWebhooks_IPNSettlesByHand(t *testing.T) {
	var mu sync.Mutex
	var got payment.Notification
	mux := http.NewServeMux()
	path := newRail().WireWebhooks(mux, func(_ context.Context, n payment.Notification) error {
		mu.Lock()
		defer mu.Unlock()
		got = n
		return nil
	})
	if path != "/webhooks/payment/mock" {
		t.Fatalf("path = %q, want the prefix the router mounts this mux at", path)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path,
		strings.NewReader(`{"ref_id":"txn_9","status":"success"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	mu.Lock()
	defer mu.Unlock()
	if got.RefID != "txn_9" || got.Status != payment.StatusSuccess {
		t.Fatalf("notification = %+v", got)
	}
}

// A body with no reference is refused rather than delivered as an empty one: settling "" would
// look up a leg nobody has.
func TestWireWebhooks_IPNRefusesABodyWithNoReference(t *testing.T) {
	mux := http.NewServeMux()
	path := newRail().WireWebhooks(mux, func(context.Context, payment.Notification) error {
		t.Error("a body with no reference was delivered")
		return nil
	})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
