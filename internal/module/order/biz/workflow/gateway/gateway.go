// Package gateway is the shared workflow engine for CheckoutWorkflow and
// FulfillmentWorkflow: the multi-attempt gateway payment loop and the
// payment-gate signal handlers. It wraps the order module core as a named
// field so that core's API isn't promoted onto the workflows that embed it.
package gateway

import (
	"context"
	"fmt"
	"time"

	orderbase "shopnexus-server/internal/module/order/biz/base"
	ordermodel "shopnexus-server/internal/module/order/model"
	orderrepo "shopnexus-server/internal/module/order/repo"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	restate "github.com/restatedev/sdk-go"
)

const (
	// paymentExpiry bounds one gateway attempt (how long a redirect URL lives).
	paymentExpiry = 30 * time.Minute
	// SessionExpiry bounds the whole session across all retry attempts.
	SessionExpiry = 24 * time.Hour
)

type Gateway struct {
	core *orderbase.Base
}

func New(core *orderbase.Base) *Gateway { return &Gateway{core} }

func paymentURLKey(attempt int) string    { return fmt.Sprintf("payment_url_%d", attempt) }
func retryKey(attempt int) string         { return fmt.Sprintf("retry_%d", attempt) }
func paymentEventKey(refID string) string { return "payment_event_" + refID }

const (
	cancelKey    = "user_cancel"
	gateStateKey = "gate"
)

// gate phases, the authoritative payment state GetPaymentURL reads (DB rows are
// a downstream projection, not the source of truth).
const (
	gateCharging  = "charging"  // attempt minted, URL not resolved yet
	gateActive    = "active"    // current attempt's URL is live
	gateRetry     = "retry"     // current attempt expired, awaiting a new-URL request
	gatePaid      = "paid"      // session settled
	gateCancelled = "cancelled" // user cancelled
	gateExpired   = "expired"   // session deadline elapsed
)

// gateState is journaled per transition by RunPaymentLoop; GetPaymentURL
// branches on it. Attempt is the current (1-based) attempt; URL its redirect.
type gateState struct {
	Attempt int    `json:"attempt"`
	URL     string `json:"url"`
	Status  string `json:"status"`
}

func (g *Gateway) setGate(ctx restate.WorkflowContext, s gateState) {
	restate.Set(ctx, gateStateKey, s)
}

type sessionFailer interface {
	MarkPaymentSessionFailed(ctx context.Context, id uuid.UUID) (ordermodel.PaymentSession, error)
	MarkPendingTxsFailedBySession(ctx context.Context, arg orderrepo.MarkPendingTxsFailedBySessionParams) error
}

// MarkSessionFailed marks the session and every still-Pending child tx Failed.
// Idempotent on already-final rows; used as a saga compensator body.
func MarkSessionFailed(ctx context.Context, q sessionFailer, sessionID uuid.UUID, reason string) error {
	if _, err := q.MarkPaymentSessionFailed(ctx, sessionID); err != nil {
		return fmt.Errorf("mark session failed: %w", err)
	}
	if err := q.MarkPendingTxsFailedBySession(ctx, orderrepo.MarkPendingTxsFailedBySessionParams{
		SessionID: sessionID,
		Error:     null.StringFrom(reason),
	}); err != nil {
		return fmt.Errorf("mark txs failed by session: %w", err)
	}
	return nil
}
