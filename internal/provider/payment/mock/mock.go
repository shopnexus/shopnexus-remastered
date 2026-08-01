// Package mock is the dev-only payment rail: a charge succeeds synchronously with no external
// call and no webhook, which is what lets a local stack complete a checkout. Selected by
// PAYMENT_PROVIDER=mock.
package mock

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"shopnexus/internal/provider/payment"
)

// Name is the PAYMENT_PROVIDER value that selects this rail.
const Name = "mock"

var _ payment.Client = (*Client)(nil)

type Client struct{}

func NewClient() payment.Client { return &Client{} }

// Charge decides immediately, direct-debit style: no redirect for a client to follow.
func (c *Client) Charge(_ context.Context, _ payment.ChargeParams) (payment.ChargeResult, error) {
	return payment.ChargeResult{ProviderID: uuid.NewString(), Status: payment.StatusSuccess}, nil
}

// WireWebhooks is a no-op: this rail answers synchronously, so there is nothing to report back.
func (c *Client) WireWebhooks(_ *http.ServeMux, _ payment.NotificationHandler) string { return "" }
