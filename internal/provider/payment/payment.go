package payment

import (
	"context"
	"encoding/json"

	stderrors "errors"
	sharedmodel "shopnexus-server/internal/shared/model"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

var ErrNotSupported = stderrors.New("operation not supported by this payment provider")

type Status string

const (
	StatusPending Status = "pending"
	StatusSuccess Status = "success"
	StatusFailed  Status = "failed"
	StatusExpired Status = "expired"
)

type ChargeParams struct {
	RefID       string
	Amount      int64
	Description string
	ReturnURL   string // redirect providers only
	Token       string // direct-debit providers only
}

// Redirect: RedirectURL set, Status=Pending. Direct-debit: URL empty, Status final.
type ChargeResult struct {
	ProviderID  string
	RedirectURL string
	Status      Status
}

type RefundParams struct {
	ProviderChargeID string
	Amount           int64
}

type RefundResult struct {
	ProviderRefundID string
	Status           Status
}

type TokenizeParams struct {
	AccountID uuid.UUID
	ReturnURL string
}

type TokenizeResult struct {
	FormURL      string          `json:"form_url,omitempty"`
	ClientConfig json.RawMessage `json:"client_config,omitempty"`
}

type Notification struct {
	RefID        string `json:"ref_id" validate:"required"`
	Status       Status `json:"status" validate:"required"`
	Amount       int64  `json:"amount,omitempty"`
	ProviderTxID string `json:"provider_tx_id,omitempty"`
}

type NotificationHandler func(ctx context.Context, n Notification) error

type Client interface {
	Config() sharedmodel.Option
	Charge(ctx context.Context, params ChargeParams) (ChargeResult, error)
	Refund(ctx context.Context, params RefundParams) (RefundResult, error)
	Tokenize(ctx context.Context, params TokenizeParams) (TokenizeResult, error)

	// WireWebhooks mounts the provider's IPN routes on Echo, delivering verified
	// notifications to `deliver`, and returns an idempotency key identifying
	// those routes. If the key already appears in `registered`, the call is a
	// no-op (returns the key without mounting). An empty key means the provider
	// has no webhooks (synchronous-only).
	WireWebhooks(e *echo.Echo, deliver NotificationHandler, registered map[string]struct{}) string
}
