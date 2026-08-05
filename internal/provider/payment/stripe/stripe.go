// Package stripe is the card rail, on Stripe Checkout.
//
// Hosted checkout, not embedded: the payer is sent to Stripe's page and comes back to `return_url`.
// The embedded flow keeps a single-page client mounted, which is worth a lot to an app that never
// navigates — but it needs a client secret and a publishable key on the response, and both clients
// here already leave the page for a redirect rail and read the outcome from the payment session on
// their way back. One shape for every redirect rail is worth more than a saved navigation.
//
// The webhook is on the payment intent rather than the checkout session, because that is the object
// that says whether money moved: a completed session with a failed intent is not a payment. Our own
// leg id travels in the intent's metadata, which is what makes the callback resolvable — the session
// id is Stripe's, and a notification has to name ours.
package stripe

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	stripesdk "github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/webhook"

	"shopnexus/internal/provider/payment"
)

// Name is what an option row's `provider` says to be served by this rail.
const Name = "stripe"

// OptionID is the row this rail publishes. Permanent once a payment has settled on it.
const OptionID = "stripe-card"

// webhookPath is where Stripe reports, under the prefix the router serves the webhook mux at.
const webhookPath = "/webhooks/payment/stripe"

// legReference is the metadata key carrying our own transaction id. Stripe hands the whole map back
// on the intent, and this is the only field in it that can find the leg to settle.
const legReference = "leg_id"

// currency is the one this platform settles in. VND is zero-decimal at Stripe, so the amount goes
// across unscaled — the ×100 every card integration reaches for would charge a hundredfold here.
const currency = "VND"

// Config is what a deployment has to hold to charge cards.
type Config struct {
	SecretKey string
	// WebhookSecret verifies Stripe's callback signature. Its own secret, and required: an
	// unverified webhook is an endpoint anybody can use to mark an order paid.
	WebhookSecret string
	// RequestTimeout bounds one API call. A required field rather than a default, like every other
	// provider deadline here — how long a vendor may take is that vendor's knowledge.
	RequestTimeout time.Duration
	// HTTPClient carries the outbound metrics and the log line. Never with a Timeout of its own:
	// that budget covers reading the body.
	HTTPClient *http.Client
	// BaseURL overrides Stripe's API host. Set only by tests.
	BaseURL string
}

var _ payment.Client = (*Client)(nil)

type Client struct {
	cfg    Config
	stripe *stripesdk.Client
	log    *slog.Logger
}

func New(cfg Config, log *slog.Logger) *Client {
	backendCfg := &stripesdk.BackendConfig{HTTPClient: cfg.HTTPClient}
	if cfg.BaseURL != "" {
		backendCfg.URL = stripesdk.String(cfg.BaseURL)
	}
	backends := stripesdk.NewBackendsWithConfig(backendCfg)
	return &Client{
		cfg:    cfg,
		stripe: stripesdk.NewClient(cfg.SecretKey, stripesdk.WithBackends(backends)),
		log:    log,
	}
}

// Options is the one row this rail owns: a card is the whole product.
func (c *Client) Options() []payment.Option {
	return []payment.Option{{
		ID:   OptionID,
		Name: "Thẻ quốc tế (Visa, Mastercard)",
		Description: "Thanh toán bằng thẻ trên trang bảo mật của Stripe. " +
			"Đơn hàng được tạo ngay khi thẻ được trừ tiền.",
		Priority: 99,
	}}
}

// Charge opens a Checkout session and hands back the page the payer completes it on.
func (c *Client) Charge(ctx context.Context, params payment.ChargeParams) (payment.ChargeResult, error) {
	if params.Currency != currency {
		return payment.ChargeResult{}, fmt.Errorf("stripe rail settles in %s, not %s", currency, params.Currency)
	}
	if params.ReturnURL == "" {
		// Stripe requires both URLs for a hosted session, and there is no sensible stand-in: a
		// payer sent to a page this platform did not choose is a payer we have lost.
		return payment.ChargeResult{}, fmt.Errorf("stripe rail needs a return URL")
	}
	ctx, cancel := context.WithTimeout(ctx, c.cfg.RequestTimeout)
	defer cancel()

	back := withRef(params.ReturnURL, params.RefID)
	description := params.Description
	if description == "" {
		description = "ShopNexus"
	}
	session, err := c.stripe.V1CheckoutSessions.Create(ctx, &stripesdk.CheckoutSessionCreateParams{
		Mode: stripesdk.String(string(stripesdk.CheckoutSessionModePayment)),
		// Both outcomes come back to the same page for the same reason SePay's do: where the payer
		// lands is not evidence of anything, and the session they read on arrival is.
		SuccessURL: stripesdk.String(back),
		CancelURL:  stripesdk.String(back),
		// Stripe's own idempotency handle on the session, and a second way to find the leg from the
		// dashboard when somebody is looking at one payment by hand.
		ClientReferenceID:  stripesdk.String(params.RefID),
		PaymentMethodTypes: []*string{stripesdk.String("card")},
		LineItems: []*stripesdk.CheckoutSessionCreateLineItemParams{{
			Quantity: stripesdk.Int64(1),
			PriceData: &stripesdk.CheckoutSessionCreateLineItemPriceDataParams{
				Currency:   stripesdk.String(strings.ToLower(currency)),
				UnitAmount: stripesdk.Int64(params.Amount),
				ProductData: &stripesdk.CheckoutSessionCreateLineItemPriceDataProductDataParams{
					Name: stripesdk.String(description),
				},
			},
		}},
		PaymentIntentData: &stripesdk.CheckoutSessionCreatePaymentIntentDataParams{
			Description: stripesdk.String(description),
			// The only thing that makes the webhook resolvable: Stripe reports on its own ids, and
			// this is where ours rides along.
			Metadata: map[string]string{legReference: params.RefID},
		},
	})
	if err != nil {
		return payment.ChargeResult{}, fmt.Errorf("create stripe checkout session: %w", err)
	}
	return payment.ChargeResult{ProviderID: session.ID, RedirectURL: session.URL}, nil
}

// WireWebhooks mounts Stripe's callback.
func (c *Client) WireWebhooks(mux *http.ServeMux, deliver payment.NotificationHandler) string {
	mux.HandleFunc("POST "+webhookPath, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "cannot read body", http.StatusBadRequest)
			return
		}
		// Verifies the signature and the timestamp window. The raw bytes, not a re-encoding: the
		// signature is over exactly what was sent.
		//
		// The version check is deliberately off. An account pinned to an API version the SDK was
		// not built for would otherwise fail every callback — and a webhook that 400s is a paid
		// card that never becomes an order, retried until Stripe gives up. The three fields read
		// below have been stable for years, so a mismatch is logged and worked with rather than
		// treated as a reason to stop taking money.
		event, err := webhook.ConstructEventWithOptions(
			body, r.Header.Get("Stripe-Signature"), c.cfg.WebhookSecret,
			webhook.ConstructEventOptions{IgnoreAPIVersionMismatch: true})
		if err != nil {
			c.log.Error("stripe webhook signature", "err", err)
			http.Error(w, "invalid signature", http.StatusBadRequest)
			return
		}
		if event.APIVersion != "" && event.APIVersion != stripesdk.APIVersion {
			c.log.Warn("stripe event from another API version",
				"event", event.APIVersion, "sdk", stripesdk.APIVersion, "type", event.Type)
		}

		status, ok := statusOf(event.Type)
		if !ok {
			// Acked: Stripe sends far more than this platform has an opinion on, and an error would
			// have it retry each one for ever.
			w.WriteHeader(http.StatusOK)
			return
		}
		var intent stripesdk.PaymentIntent
		if err := json.Unmarshal(event.Data.Raw, &intent); err != nil {
			c.log.Error("stripe webhook payload", "type", event.Type, "err", err)
			http.Error(w, "cannot parse payment intent", http.StatusBadRequest)
			return
		}
		refID := intent.Metadata[legReference]
		if refID == "" {
			// Nothing to settle: an intent created outside this platform, or one whose metadata was
			// lost. Acked rather than retried, because a redelivery will not grow the field back.
			c.log.Warn("stripe intent with no leg reference", "intent", intent.ID, "type", event.Type)
			w.WriteHeader(http.StatusOK)
			return
		}
		err = deliver(r.Context(), payment.Notification{
			RefID:        refID,
			Status:       status,
			Amount:       intent.Amount,
			ProviderTxID: intent.ID,
		})
		if err != nil {
			// 500 so Stripe retries: the card was charged, and this callback is what tells us.
			c.log.Error("settle stripe webhook", "leg", refID, "intent", intent.ID, "err", err)
			http.Error(w, "cannot settle", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	return webhookPath
}

// The events that decide a payment. On the intent rather than the session, because a session that
// completed with an unpaid intent is not money.
const (
	eventSucceeded = "payment_intent.succeeded"
	eventFailed    = "payment_intent.payment_failed"
	eventCanceled  = "payment_intent.canceled"
)

func statusOf(eventType stripesdk.EventType) (payment.Status, bool) {
	switch string(eventType) {
	case eventSucceeded:
		return payment.StatusSuccess, true
	case eventFailed, eventCanceled:
		return payment.StatusFailed, true
	default:
		return "", false
	}
}

// withRef adds the leg id to the page the payer comes back to, so it can read the outcome from the
// session rather than from anything Stripe put in the URL.
func withRef(returnURL, refID string) string {
	sep := "?"
	if strings.Contains(returnURL, "?") {
		sep = "&"
	}
	return returnURL + sep + "ref=" + refID
}
