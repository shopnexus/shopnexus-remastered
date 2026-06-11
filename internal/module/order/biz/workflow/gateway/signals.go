package gateway

import (
	"fmt"

	"shopnexus-server/internal/provider/payment"

	restate "github.com/restatedev/sdk-go"
)

// GetPaymentURL returns a usable gateway redirect URL, deciding from the
// journaled gate state (not the DB): reuse the live attempt's URL, advance to a
// fresh attempt when the current one expired, or report the terminal outcome.
// expired/cancelled are the workflow-specific terminal errors.
func (g *Gateway) GetPaymentURL(ctx restate.WorkflowSharedContext, expired, cancelled error) (string, error) {
	st, err := restate.Get[*gateState](ctx, gateStateKey)
	if err != nil {
		return "", fmt.Errorf("read gate state: %w", err)
	}
	if st == nil {
		// Loop hasn't recorded an attempt yet — await the first URL.
		return restate.Promise[string](ctx, paymentURLKey(1)).Result()
	}
	switch st.Status {
	case gateActive:
		return st.URL, nil
	case gateCharging:
		return restate.Promise[string](ctx, paymentURLKey(st.Attempt)).Result()
	case gateRetry:
		// Current attempt is dead — advance the loop (resolve is idempotent) and
		// block on the next attempt's URL.
		_ = restate.Promise[struct{}](ctx, retryKey(st.Attempt)).Resolve(struct{}{})
		return restate.Promise[string](ctx, paymentURLKey(st.Attempt+1)).Result()
	case gatePaid:
		return st.URL, nil
	case gateCancelled:
		return "", cancelled
	default: // gateExpired or any terminal
		return "", expired
	}
}

// ResolvePaymentEvent is called from the payment webhook. Late webhooks for prior
// failed attempts resolve a key no one awaits — silent no-ops.
func (g *Gateway) ResolvePaymentEvent(ctx restate.WorkflowSharedContext, noti payment.Notification) error {
	return restate.Promise[payment.Notification](ctx, paymentEventKey(noti.RefID)).Resolve(noti)
}

// Cancel aborts an in-flight payment loop.
func (g *Gateway) Cancel(ctx restate.WorkflowSharedContext) error {
	return restate.Promise[struct{}](ctx, cancelKey).Resolve(struct{}{})
}
