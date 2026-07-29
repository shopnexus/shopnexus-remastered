// Package mock is a dev-only payment provider: charges and refunds succeed
// synchronously with no external calls or webhooks. Select it with option
// Provider="mock".
package mock

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"shopnexus/internal/provider"
	finance "shopnexus/internal/provider/finance"
)

var _ finance.Client = (*Client)(nil)

type Client struct {
	config provider.Option
}

func NewClient(cfg provider.Option) finance.Client {
	return &Client{config: cfg}
}

func (c *Client) Config() provider.Option { return c.config }

// Charge immediately succeeds (direct-debit style: no redirect URL).
func (c *Client) Charge(_ context.Context, _ finance.ChargeParams) (finance.ChargeResult, error) {
	return finance.ChargeResult{ProviderID: uuid.NewString(), Status: finance.StatusSuccess}, nil
}

func (c *Client) Refund(_ context.Context, _ finance.RefundParams) (finance.RefundResult, error) {
	return finance.RefundResult{ProviderRefundID: uuid.NewString(), Status: finance.StatusSuccess}, nil
}

func (c *Client) Tokenize(_ context.Context, _ finance.TokenizeParams) (finance.TokenizeResult, error) {
	return finance.TokenizeResult{}, nil
}

// WireWebhooks is a no-op: the mock provider is synchronous-only.
func (c *Client) WireWebhooks(_ *http.ServeMux, _ finance.NotificationHandler, _ map[string]struct{}) string {
	return ""
}
