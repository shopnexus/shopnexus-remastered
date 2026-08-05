package stripe_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"shopnexus/internal/provider/payment"
	"shopnexus/internal/provider/payment/stripe"
)

const (
	webhookSecret = "whsec_test"
	returnURL     = "https://shopnexus.github.io/checkout?session_id=pay_1"
)

// stripeAPI stands in for Stripe's own host, and records the form it was sent: what this rail puts
// on the wire is the part a test can hold Stripe to.
type stripeAPI struct {
	mu     sync.Mutex
	form   url.Values
	server *httptest.Server
}

func newStripeAPI(t *testing.T) *stripeAPI {
	t.Helper()
	api := &stripeAPI{}
	api.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse stripe request: %v", err)
		}
		api.mu.Lock()
		api.form = r.PostForm
		api.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"cs_test_1","object":"checkout.session",
			"url":"https://checkout.stripe.com/c/pay/cs_test_1","payment_status":"unpaid"}`))
	}))
	t.Cleanup(api.server.Close)
	return api
}

func (a *stripeAPI) sent() url.Values {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.form
}

func newRail(baseURL string) *stripe.Client {
	return stripe.New(stripe.Config{
		SecretKey:      "sk_test_1",
		WebhookSecret:  webhookSecret,
		RequestTimeout: 5 * time.Second,
		HTTPClient:     &http.Client{},
		BaseURL:        baseURL,
	}, slog.New(slog.DiscardHandler))
}

// Hosted checkout: the payer follows Stripe's own URL and comes back to `return_url`, which is the
// one shape every redirect rail here has.
func TestCharge_HandsBackStripesHostedPage(t *testing.T) {
	api := newStripeAPI(t)

	res, err := newRail(api.server.URL).Charge(context.Background(), payment.ChargeParams{
		RefID: "txn_7", Amount: 84000, Currency: "VND",
		Description: "Ao thun", ReturnURL: returnURL,
	})
	if err != nil {
		t.Fatalf("Charge: %v", err)
	}
	if res.RedirectURL != "https://checkout.stripe.com/c/pay/cs_test_1" {
		t.Errorf("redirect = %q, want Stripe's hosted page", res.RedirectURL)
	}
	// Stripe's own id, which its callback names and an operator searches the dashboard by.
	if res.ProviderID != "cs_test_1" {
		t.Errorf("provider ref = %q, want the session id", res.ProviderID)
	}
	if res.Status != "" {
		t.Errorf("status = %q, want none from a redirect rail", res.Status)
	}
}

// VND is zero-decimal at Stripe, so the amount crosses unscaled. The ×100 every card integration
// reaches for would charge a hundred times the price.
func TestCharge_SendsTheAmountUnscaled(t *testing.T) {
	api := newStripeAPI(t)

	if _, err := newRail(api.server.URL).Charge(context.Background(), payment.ChargeParams{
		RefID: "txn_7", Amount: 84000, Currency: "VND", ReturnURL: returnURL,
	}); err != nil {
		t.Fatalf("Charge: %v", err)
	}
	form := api.sent()
	if got := form.Get("line_items[0][price_data][unit_amount]"); got != "84000" {
		t.Errorf("unit_amount = %q, want the dong figure as it stands", got)
	}
	if got := form.Get("line_items[0][price_data][currency]"); got != "vnd" {
		t.Errorf("currency = %q, want lowercase as Stripe expects", got)
	}
}

// The metadata is the only thing that makes the callback resolvable: Stripe reports on its own ids,
// so our leg id has to ride along or a paid card settles nothing.
func TestCharge_CarriesTheLegIdIntoTheIntent(t *testing.T) {
	api := newStripeAPI(t)

	if _, err := newRail(api.server.URL).Charge(context.Background(), payment.ChargeParams{
		RefID: "txn_7", Amount: 84000, Currency: "VND", ReturnURL: returnURL,
	}); err != nil {
		t.Fatalf("Charge: %v", err)
	}
	form := api.sent()
	if got := form.Get("payment_intent_data[metadata][leg_id]"); got != "txn_7" {
		t.Errorf("intent metadata leg_id = %q, want the leg id", got)
	}
	if got := form.Get("client_reference_id"); got != "txn_7" {
		t.Errorf("client_reference_id = %q, want the leg id", got)
	}
	// Both outcomes come back to the same page, carrying the leg id: where the payer lands is not
	// evidence, and the session they read on arrival is.
	for _, field := range []string{"success_url", "cancel_url"} {
		if got := form.Get(field); got != returnURL+"&ref=txn_7" {
			t.Errorf("%s = %q, want the return URL carrying the leg id", field, got)
		}
	}
}

func TestCharge_Refusals(t *testing.T) {
	api := newStripeAPI(t)

	t.Run("another currency", func(t *testing.T) {
		_, err := newRail(api.server.URL).Charge(context.Background(), payment.ChargeParams{
			RefID: "txn_7", Amount: 30, Currency: "USD", ReturnURL: returnURL,
		})
		if err == nil {
			t.Fatal("Charge succeeded for a currency this platform does not settle")
		}
	})

	// A payer sent to a page this platform did not choose is a payer we have lost, and Stripe
	// requires the URL anyway — so it is refused here rather than invented.
	t.Run("no return URL", func(t *testing.T) {
		_, err := newRail(api.server.URL).Charge(context.Background(), payment.ChargeParams{
			RefID: "txn_7", Amount: 84000, Currency: "VND",
		})
		if err == nil {
			t.Fatal("Charge succeeded with nowhere to send the payer back to")
		}
	})
}

func TestWebhook(t *testing.T) {
	cases := []struct {
		name       string
		eventType  string
		metadata   string
		sign       bool
		wantStatus int
		wantSettle *payment.Status
	}{
		{
			name: "a succeeded intent settles the leg", eventType: "payment_intent.succeeded",
			metadata: `"leg_id":"txn_7"`, sign: true,
			wantStatus: http.StatusOK, wantSettle: new(payment.StatusSuccess),
		},
		{
			name: "a failed intent fails it", eventType: "payment_intent.payment_failed",
			metadata: `"leg_id":"txn_7"`, sign: true,
			wantStatus: http.StatusOK, wantSettle: new(payment.StatusFailed),
		},
		{
			name: "a cancelled intent fails it too", eventType: "payment_intent.canceled",
			metadata: `"leg_id":"txn_7"`, sign: true,
			wantStatus: http.StatusOK, wantSettle: new(payment.StatusFailed),
		},
		{
			// Stripe sends far more than this platform has an opinion on; an error would have it
			// retry each one for ever.
			name: "an event we do not model is acked", eventType: "charge.updated",
			metadata: `"leg_id":"txn_7"`, sign: true, wantStatus: http.StatusOK,
		},
		{
			// A redelivery will not grow the field back, so retrying is pointless.
			name: "an intent with no leg id is acked", eventType: "payment_intent.succeeded",
			metadata: `"other":"x"`, sign: true, wantStatus: http.StatusOK,
		},
		{
			// The signature is the whole authentication: without it this route is a way for anyone
			// to mark an order paid.
			name: "an unsigned body settles nothing", eventType: "payment_intent.succeeded",
			metadata: `"leg_id":"txn_7"`, sign: false, wantStatus: http.StatusBadRequest,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var got []payment.Notification
			mux := http.NewServeMux()
			path := newRail("").WireWebhooks(mux, func(_ context.Context, n payment.Notification) error {
				mu.Lock()
				defer mu.Unlock()
				got = append(got, n)
				return nil
			})

			body := fmt.Sprintf(`{"id":"evt_1","type":%q,"data":{"object":
				{"id":"pi_9","object":"payment_intent","amount":84000,"metadata":{%s}}}}`,
				tc.eventType, tc.metadata)
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			if tc.sign {
				req.Header.Set("Stripe-Signature", signature(body, webhookSecret, time.Now()))
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			mu.Lock()
			defer mu.Unlock()
			if tc.wantSettle == nil {
				if len(got) != 0 {
					t.Fatalf("settled %+v, want nothing", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("settled %+v, want exactly one", got)
			}
			if got[0].Status != *tc.wantSettle || got[0].RefID != "txn_7" {
				t.Errorf("notification = %+v", got[0])
			}
			if got[0].ProviderTxID != "pi_9" {
				t.Errorf("provider ref = %q, want the intent id", got[0].ProviderTxID)
			}
		})
	}
}

// A signature from another secret is exactly the forgery this check exists for.
func TestWebhook_RefusesAnotherSecretsSignature(t *testing.T) {
	mux := http.NewServeMux()
	path := newRail("").WireWebhooks(mux, func(context.Context, payment.Notification) error {
		t.Error("a forged webhook was delivered")
		return nil
	})

	body := `{"id":"evt_1","type":"payment_intent.succeeded","data":{"object":
		{"id":"pi_9","object":"payment_intent","metadata":{"leg_id":"txn_7"}}}}`
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Stripe-Signature", signature(body, "whsec_somebody_else", time.Now()))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// The card was charged, so a settle we failed has to be retried — and Stripe's redelivery is what
// will tell us again.
func TestWebhook_AFailedSettleAsksStripeToRetry(t *testing.T) {
	mux := http.NewServeMux()
	path := newRail("").WireWebhooks(mux, func(context.Context, payment.Notification) error {
		return context.DeadlineExceeded
	})

	body := `{"id":"evt_1","type":"payment_intent.succeeded","data":{"object":
		{"id":"pi_9","object":"payment_intent","metadata":{"leg_id":"txn_7"}}}}`
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Stripe-Signature", signature(body, webhookSecret, time.Now()))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 so Stripe retries", rec.Code)
	}
}

func TestOptions_PublishesOneCardRow(t *testing.T) {
	options := newRail("").Options()

	if len(options) != 1 || options[0].ID != stripe.OptionID {
		t.Fatalf("options = %+v, want the one card row", options)
	}
	if options[0].Name == "" || options[0].Description == "" {
		t.Error("a row a buyer picks needs a name and a line of explanation")
	}
}

// signature builds the `Stripe-Signature` header the way Stripe does: HMAC-SHA256 over
// "<timestamp>.<payload>", hex, under a `v1=` part.
func signature(payload, secret string, at time.Time) string {
	ts := at.Unix()
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.%s", ts, payload)
	return fmt.Sprintf("t=%d,v1=%s", ts, hex.EncodeToString(mac.Sum(nil)))
}
