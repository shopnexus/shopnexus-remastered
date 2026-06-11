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
	orderdb "shopnexus-server/internal/module/order/db/sqlc"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
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

// Promise / state keys shared between the loop and the signal handlers.
func paymentURLKey(attempt int) string   { return fmt.Sprintf("payment_url_%d", attempt) }
func retryKey(attempt int) string        { return fmt.Sprintf("retry_%d", attempt) }
func paymentEventKey(refID string) string { return "payment_event_" + refID }

const (
	cancelKey         = "user_cancel"
	paymentAttemptKey = "payment_attempt"
)

// MarkSessionFailed marks the session and every still-Pending child tx Failed.
// Idempotent on already-final rows; used as a saga compensator body.
func MarkSessionFailed(ctx context.Context, q orderdb.Querier, sessionID uuid.UUID, reason string) error {
	if _, err := q.MarkPaymentSessionFailed(ctx, sessionID); err != nil {
		return fmt.Errorf("mark session failed: %w", err)
	}
	if err := q.MarkPendingTxsFailedBySession(ctx, orderdb.MarkPendingTxsFailedBySessionParams{
		SessionID: sessionID,
		Error:     null.StringFrom(reason),
	}); err != nil {
		return fmt.Errorf("mark txs failed by session: %w", err)
	}
	return nil
}
