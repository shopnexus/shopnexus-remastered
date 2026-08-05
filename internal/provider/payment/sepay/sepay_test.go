package sepay_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"shopnexus/internal/provider/payment"
	"shopnexus/internal/provider/payment/sepay"
)

const (
	base      = "https://shopnexus.test/api/v1"
	signKey   = "sign-secret"
	ipnKey    = "ipn-secret"
	returnURL = "https://shopnexus.github.io/checkout?session_id=pay_1"
)

func newRail() *sepay.Client {
	return sepay.New(sepay.Config{
		MerchantID:   "merchant-1",
		SecretKey:    signKey,
		IPNSecretKey: ipnKey,
		BaseURL:      base,
		Sandbox:      true,
	}, slog.New(slog.DiscardHandler))
}

func charge(t *testing.T, currency string) payment.ChargeResult {
	t.Helper()
	res, err := newRail().Charge(context.Background(), payment.ChargeParams{
		RefID: "txn_7", Option: sepay.OptionID, Amount: 84000,
		Currency: currency, Description: "Ao thun", ReturnURL: returnURL,
	})
	if err != nil {
		t.Fatalf("Charge: %v", err)
	}
	return res
}

// The redirect is this gateway's own page, not SePay's: SePay's init is POST-only and binds the
// checkout to the payer's browser, so a server-side POST would open one they cannot finish.
func TestCharge_RedirectsThroughOurOwnPage(t *testing.T) {
	res := charge(t, "VND")

	if !strings.HasPrefix(res.RedirectURL, base+"/webhooks/payment/sepay/checkout?") {
		t.Fatalf("redirect = %q, want this gateway's own checkout page", res.RedirectURL)
	}
	// The leg id is the reference SePay's callback will name, so it is worth recording now.
	if res.ProviderID != "txn_7" {
		t.Errorf("provider ref = %q, want the leg id", res.ProviderID)
	}
	// A redirect rail decides nothing on the spot; the IPN does.
	if res.Status != "" {
		t.Errorf("status = %q, want none from a redirect rail", res.Status)
	}
}

// The signature is over the fields in a fixed order — this is the one thing a mistake in leaves a
// page that looks right and that SePay refuses.
func TestCharge_SignsTheFieldsInSePaysOrder(t *testing.T) {
	res := charge(t, "VND")
	query := mustQuery(t, res.RedirectURL)

	want := hmacBase64(strings.Join([]string{
		"merchant=merchant-1",
		"currency=VND",
		"order_amount=84000",
		"operation=PURCHASE",
		"order_description=Ao thun",
		"payment_method=BANK_TRANSFER",
		"order_invoice_number=txn_7",
		"success_url=" + returnURL + "&ref=txn_7",
		"error_url=" + returnURL + "&ref=txn_7",
		"cancel_url=" + returnURL + "&ref=txn_7",
	}, ","), signKey)

	if got := query.Get("signature"); got != want {
		t.Errorf("signature = %q, want %q", got, want)
	}
}

// All three outcomes go to one page on purpose: where the payer lands says nothing about whether the
// money arrived, so a client reading the outcome from it would be reading a forgeable claim.
func TestCharge_EveryOutcomeReturnsToTheSamePage(t *testing.T) {
	query := mustQuery(t, charge(t, "VND").RedirectURL)

	for _, field := range []string{"success_url", "error_url", "cancel_url"} {
		if got := query.Get(field); got != returnURL+"&ref=txn_7" {
			t.Errorf("%s = %q, want the return URL carrying the leg id", field, got)
		}
	}
}

// A rail that settles in dong must refuse anything else rather than reading the number as dong.
func TestCharge_RefusesAnotherCurrency(t *testing.T) {
	_, err := newRail().Charge(context.Background(), payment.ChargeParams{
		RefID: "txn_7", Amount: 30, Currency: "USD", ReturnURL: returnURL,
	})
	if err == nil {
		t.Fatal("Charge succeeded, want a refusal for a currency SePay does not settle")
	}
}

// The page has to submit the fields in the order they were signed, and carry the signature last.
func TestCheckoutPage_SubmitsTheSignedFieldsInOrder(t *testing.T) {
	mux := http.NewServeMux()
	newRail().WireWebhooks(mux, func(context.Context, payment.Notification) error { return nil })

	target := mustQuery(t, charge(t, "VND").RedirectURL)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/webhooks/payment/sepay/checkout?"+target.Encode(), nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	// The sandbox host, because that is what this client was configured for.
	if !strings.Contains(body, `action="https://pay-sandbox.sepay.vn/v1/checkout/init"`) {
		t.Error("the form does not post to the sandbox host")
	}
	order := []string{
		"merchant", "currency", "order_amount", "operation", "order_description",
		"payment_method", "order_invoice_number", "success_url", "error_url", "cancel_url",
		"signature",
	}
	at := -1
	for _, name := range order {
		i := strings.Index(body, `name="`+name+`"`)
		if i < 0 {
			t.Fatalf("the form is missing %q", name)
		}
		if i < at {
			t.Fatalf("%q is out of order, so SePay will refuse the signature", name)
		}
		at = i
	}
}

// A page with no signed payload is refused rather than rendered: an empty form posted to SePay is a
// checkout nobody can complete, and it would look like our bug to the payer.
func TestCheckoutPage_RefusesAnUnsignedRequest(t *testing.T) {
	mux := http.NewServeMux()
	newRail().WireWebhooks(mux, func(context.Context, payment.Notification) error { return nil })

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/webhooks/payment/sepay/checkout", nil))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestIPN(t *testing.T) {
	cases := []struct {
		name       string
		secret     string
		body       string
		wantStatus int
		wantSettle *payment.Status
	}{
		{
			name: "a paid order settles the leg", secret: ipnKey,
			body: `{"notification_type":"ORDER_PAID","order":{"order_invoice_number":"txn_7",
			        "order_amount":"84000"},"transaction":{"transaction_id":"sp_99"}}`,
			wantStatus: http.StatusOK, wantSettle: new(payment.StatusSuccess),
		},
		{
			name: "a voided transaction fails it", secret: ipnKey,
			body: `{"notification_type":"TRANSACTION_VOID","order":{"order_invoice_number":"txn_7",
			        "order_amount":"84000"},"transaction":{"transaction_id":"sp_99"}}`,
			wantStatus: http.StatusOK, wantSettle: new(payment.StatusFailed),
		},
		{
			// Acked, or SePay retries it for ever.
			name: "a notification this platform has no opinion on is acked", secret: ipnKey,
			body:       `{"notification_type":"ORDER_CREATED","order":{"order_invoice_number":"txn_7"}}`,
			wantStatus: http.StatusOK,
		},
		{
			// The secret is the whole authentication: without this check anyone could mark an
			// order paid.
			name: "a wrong secret settles nothing", secret: "guessed",
			body:       `{"notification_type":"ORDER_PAID","order":{"order_invoice_number":"txn_7"}}`,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "a body with no invoice number is refused", secret: ipnKey,
			body:       `{"notification_type":"ORDER_PAID","order":{}}`,
			wantStatus: http.StatusBadRequest,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var got []payment.Notification
			mux := http.NewServeMux()
			path := newRail().WireWebhooks(mux, func(_ context.Context, n payment.Notification) error {
				mu.Lock()
				defer mu.Unlock()
				got = append(got, n)
				return nil
			})

			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(tc.body))
			req.Header.Set("X-Secret-Key", tc.secret)
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
			if got[0].ProviderTxID != "sp_99" {
				t.Errorf("provider ref = %q, want SePay's own", got[0].ProviderTxID)
			}
		})
	}
}

// A settle that failed has to be retried, and its IPN is the only thing that will tell us again.
func TestIPN_AFailedSettleAsksSePayToRetry(t *testing.T) {
	mux := http.NewServeMux()
	path := newRail().WireWebhooks(mux, func(context.Context, payment.Notification) error {
		return context.DeadlineExceeded
	})

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(
		`{"notification_type":"ORDER_PAID","order":{"order_invoice_number":"txn_7"}}`))
	req.Header.Set("X-Secret-Key", ipnKey)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 so SePay retries", rec.Code)
	}
}

// The row is the code's, so it exists wherever this rail is registered.
func TestOptions_PublishesOneBankTransferRow(t *testing.T) {
	options := newRail().Options()

	if len(options) != 1 || options[0].ID != sepay.OptionID {
		t.Fatalf("options = %+v, want the one bank-transfer row", options)
	}
	if options[0].Name == "" || options[0].Description == "" {
		t.Error("a row a buyer picks needs a name and a line of explanation")
	}
}

func mustQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return parsed.Query()
}

func hmacBase64(data, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(data))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
