package gateway

import (
	"fmt"

	"shopnexus-server/internal/provider/payment"

	restate "github.com/restatedev/sdk-go"
)

// WaitFirstURL blocks until the loop resolves the first attempt's URL. Bridges
// the async workflow submit into a synchronous redirect for the HTTP caller.
func (g *Gateway) WaitFirstURL(ctx restate.WorkflowSharedContext) (string, error) {
	return restate.Promise[string](ctx, paymentURLKey(1)).Result()
}

// RequestNewURL is the multi-attempt entry point. The caller has verified the
// latest gateway tx is Failed/expired. Resolving the current attempt's retry
// promise (idempotent) advances the loop to attempt+1; we then block on its
// URL. expired is the workflow-specific terminal error for a dead session.
func (g *Gateway) RequestNewURL(ctx restate.WorkflowSharedContext, expired error) (string, error) {
	attempt, err := restate.Get[int](ctx, paymentAttemptKey)
	if err != nil {
		return "", fmt.Errorf("read payment_attempt: %w", err)
	}
	if attempt < 1 {
		return "", expired
	}
	_ = restate.Promise[struct{}](ctx, retryKey(attempt)).Resolve(struct{}{})
	return restate.Promise[string](ctx, paymentURLKey(attempt+1)).Result()
}

// ResolvePaymentEvent is called from the payment webhook. The notification's
// RefID is the gateway tx UUID, so late webhooks for already-Failed prior
// attempts resolve a key no one awaits — silent no-ops.
func (g *Gateway) ResolvePaymentEvent(ctx restate.WorkflowSharedContext, noti payment.Notification) error {
	return restate.Promise[payment.Notification](ctx, paymentEventKey(noti.RefID)).Resolve(noti)
}

// Cancel aborts an in-flight payment loop.
func (g *Gateway) Cancel(ctx restate.WorkflowSharedContext) error {
	return restate.Promise[struct{}](ctx, cancelKey).Resolve(struct{}{})
}
