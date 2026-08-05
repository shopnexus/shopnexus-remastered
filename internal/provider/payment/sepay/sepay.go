// Package sepay is the SePay rail: a Vietnamese bank-transfer gateway the payer completes on
// SePay's own hosted page.
//
// Two things shape this file. First, `Charge` makes no HTTP call — SePay's checkout is opened by
// the *browser*, so charging is building a signed set of form fields and handing back a URL.
// Second, that URL cannot be SePay's: `/v1/checkout/init` is POST-only and ties the session to the
// payer's own browser (cookies, IP), so a server-side POST would open a checkout the payer cannot
// finish. This package therefore serves a page of its own that carries the fields and submits
// itself — the redirect a payer follows is ours, and the POST that lands them on SePay is theirs.
//
// The signature is over the fields in a fixed order, so the form's input order has to match: map
// iteration would produce a valid-looking page SePay rejects.
package sepay

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"shopnexus/internal/provider/payment"
)

// Name is what an option row's `provider` says to be served by this rail.
const Name = "sepay"

// OptionID is the row this rail publishes. Permanent once a payment has settled on it.
const OptionID = "sepay-bank-transfer"

// SePay's own endpoints. The sandbox is a different host rather than a flag on the request, so the
// choice is made once here instead of at every call.
const (
	sandboxCheckoutURL = "https://pay-sandbox.sepay.vn/v1/checkout/init"
	liveCheckoutURL    = "https://pay.sepay.vn/v1/checkout/init"
)

// The routes this rail mounts, under the prefix the router serves the webhook mux at.
const (
	checkoutPath = "/webhooks/payment/sepay/checkout"
	webhookPath  = "/webhooks/payment/sepay"
)

// currency is the only one SePay settles in. Checked rather than assumed: sending a USD amount to a
// rail that reads it as dong would charge the payer twenty-four thousand times too little.
const currency = "VND"

// Config is what a deployment has to hold to charge through SePay.
type Config struct {
	MerchantID string
	// SecretKey signs the checkout fields; IPNSecretKey authenticates SePay's callback to us. Two
	// different secrets on purpose — they travel in opposite directions.
	SecretKey    string
	IPNSecretKey string
	// BaseURL is where this gateway answers as a browser sees it: the redirect handed to the payer
	// has to be absolute, and a client on another origin cannot follow a relative one.
	BaseURL string
	// Sandbox picks SePay's test host. Not a default — a deployment that thinks it is taking real
	// transfers and is not would be discovered by the seller who was never paid.
	Sandbox bool
}

var _ payment.Client = (*Client)(nil)

type Client struct {
	cfg         Config
	checkoutURL string
	log         *slog.Logger
}

func New(cfg Config, log *slog.Logger) *Client {
	checkoutURL := liveCheckoutURL
	if cfg.Sandbox {
		checkoutURL = sandboxCheckoutURL
	}
	return &Client{cfg: cfg, checkoutURL: checkoutURL, log: log}
}

// Options is the one row this rail owns. A bank transfer is the whole product — there is nothing
// per-rail for an operator to arrange — so the code declares it rather than leaving it to be
// inserted by hand.
func (c *Client) Options() []payment.Option {
	return []payment.Option{{
		ID:   OptionID,
		Name: "Chuyển khoản ngân hàng (SePay)",
		Description: "Chuyển khoản qua ứng dụng ngân hàng. " +
			"Đơn hàng được tạo ngay khi SePay xác nhận đã nhận tiền.",
		Priority: 100,
	}}
}

// Charge builds the signed checkout and hands back the page that submits it.
func (c *Client) Charge(_ context.Context, params payment.ChargeParams) (payment.ChargeResult, error) {
	if params.Currency != currency {
		return payment.ChargeResult{}, fmt.Errorf("sepay settles in %s, not %s", currency, params.Currency)
	}
	fields := c.checkoutFields(params)
	query := url.Values{}
	for _, f := range fields {
		query.Set(f.key, f.value)
	}
	query.Set("signature", sign(fields, c.cfg.SecretKey))

	return payment.ChargeResult{
		// The reference we gave SePay is our own leg id, which is what its callback names — so it
		// is also the provider reference worth recording before one exists.
		ProviderID:  params.RefID,
		RedirectURL: strings.TrimRight(c.cfg.BaseURL, "/") + checkoutPath + "?" + query.Encode(),
	}, nil
}

// checkoutFields is the payload SePay signs and reads, in the order it signs it.
func (c *Client) checkoutFields(params payment.ChargeParams) []field {
	fields := []field{
		{"merchant", c.cfg.MerchantID},
		{"currency", currency},
		{"order_amount", strconv.FormatInt(params.Amount, 10)},
		{"operation", "PURCHASE"},
		{"order_description", params.Description},
		{"payment_method", "BANK_TRANSFER"},
		{"order_invoice_number", params.RefID},
	}
	if params.ReturnURL == "" {
		return fields
	}
	// All three outcomes land on the same page: where the payer is sent says nothing about whether
	// the money arrived — only the IPN does — so a client that read the outcome from the URL it was
	// returned to would be reading a claim anybody can forge.
	back := withRef(params.ReturnURL, params.RefID)
	return append(fields,
		field{"success_url", back},
		field{"error_url", back},
		field{"cancel_url", back},
	)
}

// fieldOrder is what the self-submitting form emits, and it must stay the order `sign` used:
// SePay verifies the signature against the submitted body in submission order.
var fieldOrder = []string{
	"merchant", "currency", "order_amount", "operation", "order_description",
	"payment_method", "order_invoice_number", "customer_id",
	"success_url", "error_url", "cancel_url",
}

// WireWebhooks mounts the self-submitting checkout page and SePay's IPN.
func (c *Client) WireWebhooks(mux *http.ServeMux, deliver payment.NotificationHandler) string {
	mux.HandleFunc("GET "+checkoutPath, c.serveCheckout)
	mux.HandleFunc("POST "+webhookPath, c.serveIPN(deliver))
	return webhookPath
}

func (c *Client) serveCheckout(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if query.Get("order_invoice_number") == "" || query.Get("signature") == "" {
		http.Error(w, "not a signed checkout", http.StatusBadRequest)
		return
	}
	inputs := make([]field, 0, len(fieldOrder)+1)
	for _, name := range fieldOrder {
		if v := query.Get(name); v != "" {
			inputs = append(inputs, field{name, v})
		}
	}
	// Appended last, exactly as it was signed last, and separately because it is not one of the
	// signed fields.
	inputs = append(inputs, field{"signature", query.Get("signature")})

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := checkoutPage.Execute(w, map[string]any{
		"Action": c.checkoutURL,
		"Fields": inputs,
	}); err != nil {
		c.log.Error("render sepay checkout page", "err", err)
	}
}

// serveIPN is SePay reporting the outcome. The shared secret is checked before the body is read: an
// unauthenticated caller must not be able to settle a leg, and must not learn what we do with one.
func (c *Client) serveIPN(deliver payment.NotificationHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Secret-Key") != c.cfg.IPNSecretKey {
			c.log.Error("sepay IPN with a wrong secret", "remote", r.RemoteAddr)
			ack(w, http.StatusUnauthorized, false)
			return
		}
		var body ipn
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			c.log.Error("decode sepay IPN", "err", err)
			ack(w, http.StatusBadRequest, false)
			return
		}
		if body.Order.InvoiceNumber == "" {
			c.log.Error("sepay IPN with no invoice number", "notification", body.NotificationType)
			ack(w, http.StatusBadRequest, false)
			return
		}
		status, known := statusOf(body.NotificationType)
		if !known {
			// Acked, not refused: SePay sends notifications this platform has no opinion on, and
			// answering an error would have it retry them for ever.
			c.log.Info("sepay IPN ignored",
				"notification", body.NotificationType, "invoice", body.Order.InvoiceNumber)
			ack(w, http.StatusOK, true)
			return
		}
		// SePay sends the amount as a string. It is passed through for the record and never
		// trusted: the leg settles on its own figure.
		amount, _ := strconv.ParseInt(body.Order.Amount, 10, 64)
		err := deliver(r.Context(), payment.Notification{
			RefID:        body.Order.InvoiceNumber,
			Status:       status,
			Amount:       amount,
			ProviderTxID: body.Transaction.ID,
		})
		if err != nil {
			// 500 so SePay retries: a leg we failed to settle is money that moved with nothing to
			// show for it, and its IPN is the only thing that will tell us again.
			c.log.Error("settle sepay IPN", "invoice", body.Order.InvoiceNumber, "err", err)
			ack(w, http.StatusInternalServerError, false)
			return
		}
		ack(w, http.StatusOK, true)
	}
}

// ipn is SePay's callback, reduced to what settles a leg.
type ipn struct {
	NotificationType string `json:"notification_type"`
	Order            struct {
		Status        string `json:"order_status"`
		InvoiceNumber string `json:"order_invoice_number"`
		Amount        string `json:"order_amount"`
	} `json:"order"`
	Transaction struct {
		ID     string `json:"transaction_id"`
		Status string `json:"transaction_status"`
	} `json:"transaction"`
}

// The notification types that decide a payment. Anything else is somebody else's business.
const (
	notifyPaid = "ORDER_PAID"
	notifyVoid = "TRANSACTION_VOID"
)

func statusOf(notificationType string) (payment.Status, bool) {
	switch notificationType {
	case notifyPaid:
		return payment.StatusSuccess, true
	case notifyVoid:
		return payment.StatusFailed, true
	default:
		return "", false
	}
}

func ack(w http.ResponseWriter, status int, ok bool) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"success":%t}`, ok)
}

type field struct{ key, value string }

// Key and Value are exported for the template, which cannot reach unexported fields.
func (f field) Key() string   { return f.key }
func (f field) Value() string { return f.value }

// sign is SePay's checkout signature: the non-empty fields as `k=v` joined by commas, HMAC-SHA256,
// base64. Order is the caller's, because it is part of what is signed.
func sign(fields []field, secret string) string {
	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		if f.value != "" {
			parts = append(parts, f.key+"="+f.value)
		}
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strings.Join(parts, ",")))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// withRef adds the leg id to the page the payer comes back to, so it can read the outcome from the
// session rather than from anything SePay put in the URL.
func withRef(returnURL, refID string) string {
	sep := "?"
	if strings.Contains(returnURL, "?") {
		sep = "&"
	}
	return returnURL + sep + "ref=" + url.QueryEscape(refID)
}

// checkoutPage carries the signed fields to SePay under the payer's own browser. html/template
// escapes the values, which matters: the description is a seller's text and the return URL is a
// client's, and both come back through a query string.
var checkoutPage = template.Must(template.New("sepay").Parse(`<!doctype html>
<meta charset="utf-8"><title>Đang chuyển tới SePay…</title>
<body style="font:16px/1.5 system-ui,sans-serif;text-align:center;margin-top:20vh">
<p>Đang chuyển tới SePay…</p>
<form id="f" method="POST" action="{{.Action}}">
{{range .Fields}}<input type="hidden" name="{{.Key}}" value="{{.Value}}">
{{end}}<noscript><button type="submit">Tiếp tục tới SePay</button></noscript>
</form>
<script>document.getElementById('f').submit();</script>
`))
