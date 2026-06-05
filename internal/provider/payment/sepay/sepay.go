package sepay

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"shopnexus-server/internal/provider/payment"
	sharedmodel "shopnexus-server/internal/shared/model"

	"github.com/labstack/echo/v4"
)

var _ payment.Client = (*ClientImpl)(nil)

const (
	sandboxCheckoutURL = "https://pay-sandbox.sepay.vn/v1/checkout/init"
	prodCheckoutURL    = "https://pay.sepay.vn/v1/checkout/init"

	proxyCheckoutPath = "/api/v1/payment/checkout/sepay"
)

type Data struct {
	MerchantID    string `json:"merchant_id"`
	SecretKey     string `json:"secret_key"`
	IPNSecretKey  string `json:"ipn_secret_key"`
	PublicBaseURL string `json:"public_base_url"`
	ReturnURL     string `json:"return_url"`
	Sandbox       bool   `json:"sandbox"`
}

type ClientImpl struct {
	config      sharedmodel.Option
	data        Data
	checkoutURL string
}

func NewClient(cfg sharedmodel.Option) payment.Client {
	var data Data
	if len(cfg.Data) > 0 {
		_ = json.Unmarshal(cfg.Data, &data)
	}
	checkoutURL := prodCheckoutURL
	if data.Sandbox {
		checkoutURL = sandboxCheckoutURL
	}
	return &ClientImpl{config: cfg, data: data, checkoutURL: checkoutURL}
}

func (c *ClientImpl) Config() sharedmodel.Option {
	return c.config
}

func (c *ClientImpl) Charge(ctx context.Context, params payment.ChargeParams) (payment.ChargeResult, error) {
	invoiceNumber := params.RefID

	returnURL := params.ReturnURL
	if returnURL == "" {
		returnURL = c.data.ReturnURL
	}

	// Field order matches SePay PHP SDK's signCheckoutFields allowlist iteration:
	// merchant, currency, order_amount, operation, order_description,
	// payment_method, order_invoice_number, customer_id,
	// success_url, error_url, cancel_url.
	// HMAC input is "k1=v1,k2=v2,..." in this exact order.
	fields := []keyValue{
		{"merchant", c.data.MerchantID},
		{"currency", "VND"},
		{"order_amount", fmt.Sprintf("%d", params.Amount)},
		{"operation", "PURCHASE"},
		{"order_description", params.Description},
		{"payment_method", "BANK_TRANSFER"},
		{"order_invoice_number", invoiceNumber},
	}
	if returnURL != "" {
		// All three redirect URLs carry only the transaction ref. The FE
		// fetches the transaction row and derives outcome from its status.
		fields = append(fields,
			keyValue{"success_url", addRef(returnURL, invoiceNumber)},
			keyValue{"error_url", addRef(returnURL, invoiceNumber)},
			keyValue{"cancel_url", addRef(returnURL, invoiceNumber)},
		)
	}

	sig := signFields(fields, c.data.SecretKey)

	q := url.Values{}
	for _, kv := range fields {
		q.Set(kv.key, kv.value)
	}
	q.Set("signature", sig)

	return payment.ChargeResult{
		ProviderID:  invoiceNumber,
		RedirectURL: strings.TrimRight(c.data.PublicBaseURL, "/") + proxyCheckoutPath + "?" + q.Encode(),
		Status:      payment.StatusPending,
	}, nil
}

func (c *ClientImpl) Refund(ctx context.Context, params payment.RefundParams) (payment.RefundResult, error) {
	return payment.RefundResult{}, payment.ErrNotSupported
}

func (c *ClientImpl) Tokenize(ctx context.Context, params payment.TokenizeParams) (payment.TokenizeResult, error) {
	return payment.TokenizeResult{}, payment.ErrNotSupported
}

// checkoutFieldOrder is the canonical field order required for SePay's signature.
// Mirror SePay PHP SDK's CheckoutResource::prepareFormFields insertion order
// (excludes `signature`, which is appended last as a separate form input).
var checkoutFieldOrder = []string{
	"merchant",
	"currency",
	"order_amount",
	"operation",
	"order_description",
	"payment_method",
	"order_invoice_number",
	"customer_id",
	"success_url",
	"error_url",
	"cancel_url",
}

const (
	notifTypeOrderPaid       = "ORDER_PAID"
	notifTypeTransactionVoid = "TRANSACTION_VOID"
)

type ipnPayload struct {
	NotificationType string `json:"notification_type"`
	Order            struct {
		OrderStatus        string `json:"order_status"`
		OrderInvoiceNumber string `json:"order_invoice_number"`
		OrderAmount        string `json:"order_amount"`
	} `json:"order"`
	Transaction struct {
		TransactionID     string `json:"transaction_id"`
		TransactionStatus string `json:"transaction_status"`
	} `json:"transaction"`
}

func (c *ClientImpl) WireWebhooks(e *echo.Echo, deliver payment.NotificationHandler, registered map[string]struct{}) string {
	const key = "payment/sepay"
	if _, ok := registered[key]; ok {
		return key
	}

	// SePay /v1/checkout/init is POST-only and expects a browser-submitted form
	// (session cookies + IP fingerprint tied to the user's browser). This route
	// renders an auto-submitting HTML form so navigation lands the user on
	// SePay's domain with their own session.
	//
	// SePay verifies the signature against the POST body in submission order, so
	// the form's input element order MUST match the canonical sign order; map
	// iteration is non-deterministic and breaks signature verification.
	e.GET(proxyCheckoutPath, func(ec echo.Context) error {
		q := ec.Request().URL.Query()
		if len(q) == 0 {
			return ec.NoContent(http.StatusBadRequest)
		}

		var buf strings.Builder
		buf.WriteString(`<!doctype html><html><head><meta charset="utf-8"><title>Redirecting to SePay…</title></head><body><form id="sp" method="POST" action="`)
		buf.WriteString(html.EscapeString(c.checkoutURL))
		buf.WriteString(`">`)
		for _, name := range checkoutFieldOrder {
			v := q.Get(name)
			if v == "" {
				continue
			}
			buf.WriteString(`<input type="hidden" name="`)
			buf.WriteString(html.EscapeString(name))
			buf.WriteString(`" value="`)
			buf.WriteString(html.EscapeString(v))
			buf.WriteString(`">`)
		}
		if sig := q.Get("signature"); sig != "" {
			buf.WriteString(`<input type="hidden" name="signature" value="`)
			buf.WriteString(html.EscapeString(sig))
			buf.WriteString(`">`)
		}
		buf.WriteString(`<noscript><button type="submit">Continue to SePay</button></noscript></form><script>document.getElementById('sp').submit();</script></body></html>`)

		return ec.HTML(http.StatusOK, buf.String())
	})

	e.POST("/api/v1/payment/webhook/sepay", func(ec echo.Context) error {
		if ec.Request().Header.Get("X-Secret-Key") != c.data.IPNSecretKey {
			slog.Error("sepay webhook: invalid secret key")
			return ec.JSON(http.StatusUnauthorized, map[string]bool{"success": false})
		}

		var payload ipnPayload
		if err := json.NewDecoder(ec.Request().Body).Decode(&payload); err != nil {
			slog.Error("sepay webhook: decode body", slog.Any("error", err))
			return ec.JSON(http.StatusBadRequest, map[string]bool{"success": false})
		}

		invoiceNumber := payload.Order.OrderInvoiceNumber
		if invoiceNumber == "" {
			slog.Error("sepay webhook: missing order_invoice_number")
			return ec.JSON(http.StatusBadRequest, map[string]bool{"success": false})
		}

		status, ok := mapNotificationType(payload.NotificationType)
		if !ok {
			slog.Warn("sepay webhook: unknown notification_type",
				slog.String("notification_type", payload.NotificationType),
				slog.String("invoice", invoiceNumber))
			return ec.JSON(http.StatusOK, map[string]bool{"success": true})
		}

		amount, _ := strconv.ParseInt(payload.Order.OrderAmount, 10, 64)

		notification := payment.Notification{
			RefID:        invoiceNumber,
			Status:       status,
			Amount:       amount,
			ProviderTxID: payload.Transaction.TransactionID,
		}

		if err := deliver(ec.Request().Context(), notification); err != nil {
			slog.Error("sepay webhook: deliver error", slog.Any("error", err))
		}

		return ec.JSON(http.StatusOK, map[string]bool{"success": true})
	})
	return key
}

func mapNotificationType(t string) (payment.Status, bool) {
	switch t {
	case notifTypeOrderPaid:
		return payment.StatusSuccess, true
	case notifTypeTransactionVoid:
		return payment.StatusFailed, true
	default:
		return "", false
	}
}
