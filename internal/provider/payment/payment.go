// Package payment defines the payment provider seam: how this platform charges a payer and how
// that provider reports the outcome back.
//
// One method and one webhook. A refund is not here on purpose — a granted refund moves money
// inside finance's own ledger (the escrow goes back to the buyer's wallet), so no rail is asked
// to reverse anything; when one is, `Refund` arrives with the flow that needs it.
package payment

import (
	"context"
	"net/http"
)

// Status is a payment's outcome as the rail reports it. Kebab-case like every enum-ish value here.
type Status string

const (
	StatusSuccess Status = "success"
	StatusFailed  Status = "failed"
)

type ChargeParams struct {
	RefID       string
	Amount      int64
	Description string
	ReturnURL   string // redirect providers only
	Token       string // direct-debit providers only
}

// ChargeResult is either a redirect to follow or a decided charge: a redirect rail answers with
// RedirectURL set and no final status, a direct-debit rail with an empty URL and the outcome.
type ChargeResult struct {
	ProviderID  string
	RedirectURL string
	Status      Status
}

// Notification is what a rail's webhook carries, reduced to what finance settles on.
type Notification struct {
	RefID        string `json:"ref_id" validate:"required"`
	Status       Status `json:"status" validate:"required"`
	Amount       int64  `json:"amount,omitempty"`
	ProviderTxID string `json:"provider_tx_id,omitempty"`
}

type NotificationHandler func(ctx context.Context, n Notification) error

type Client interface {
	Charge(ctx context.Context, params ChargeParams) (ChargeResult, error)
	// WireWebhooks mounts the provider's own IPN route and answers with the path it took, which
	// is what the composition root logs.
	WireWebhooks(mux *http.ServeMux, deliver NotificationHandler) string
}
