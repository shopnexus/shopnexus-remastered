package checkout

import (
	"fmt"

	restate "github.com/restatedev/sdk-go"

	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/provider/payment"
)

// WaitPaymentURL blocks until Run resolves the FIRST attempt's URL. Used by
// the sync /buyer/checkout HTTP handler to bridge the async workflow submit
// into a redirect response. Subsequent retries go through RequestNewPaymentURL.
func (h *CheckoutWorkflow) WaitPaymentURL(
	ctx restate.WorkflowSharedContext,
	_ struct{},
) (string, error) {
	return restate.Promise[string](ctx, "payment_url_1").Result()
}

// RequestNewPaymentURL is the multi-attempt entry point. Caller (the
// /buyer/checkout/:sessionID/payment-url echo endpoint) has already verified
// the latest gateway tx is Failed/expired before calling this. We resolve the
// current attempt's retry promise (idempotent) so Run advances to attempt+1,
// then block on the new URL. If the user races us, double-resolve is silent.
func (h *CheckoutWorkflow) RequestNewPaymentURL(
	ctx restate.WorkflowSharedContext,
	_ struct{},
) (string, error) {
	attempt, err := restate.Get[int](ctx, "payment_attempt")
	if err != nil {
		return "", fmt.Errorf("read payment_attempt state: %w", err)
	}
	if attempt < 1 {
		return "", ordermodel.ErrCheckoutExpired
	}
	_ = restate.Promise[struct{}](ctx, fmt.Sprintf("retry_%d", attempt)).Resolve(struct{}{})
	return restate.Promise[string](ctx, fmt.Sprintf("payment_url_%d", attempt+1)).Result()
}

// PaymentNotification is called by the payment provider via OrderHandler.
// OnPaymentResult. The webhook's RefID is the gateway tx UUID — we key the
// promise by it so late webhooks for already-Failed prior attempts are
// silently no-ops (no-one's awaiting the old key).
func (h *CheckoutWorkflow) PaymentNotification(
	ctx restate.WorkflowSharedContext,
	noti payment.Notification,
) error {
	return restate.Promise[payment.Notification](ctx, "payment_event_"+noti.RefID).Resolve(noti)
}

// CancelCheckout lets the buyer abort an in-flight checkout
func (h *CheckoutWorkflow) CancelCheckout(
	ctx restate.WorkflowSharedContext,
	_ struct{},
) error {
	return restate.Promise[struct{}](ctx, "user_cancel").Resolve(struct{}{})
}

