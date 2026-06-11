package checkout

import (
	restate "github.com/restatedev/sdk-go"

	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/provider/payment"
)

// WaitPaymentURL blocks until Run resolves the first attempt's URL — the bridge
// for the sync /buyer/checkout HTTP handler.
func (h *CheckoutWorkflow) WaitPaymentURL(ctx restate.WorkflowSharedContext, _ struct{}) (string, error) {
	return h.gw.WaitFirstURL(ctx)
}

// RequestNewPaymentURL is the multi-attempt retry entry point.
func (h *CheckoutWorkflow) RequestNewPaymentURL(ctx restate.WorkflowSharedContext, _ struct{}) (string, error) {
	return h.gw.RequestNewURL(ctx, ordermodel.ErrCheckoutExpired)
}

// PaymentNotification is called by the payment provider via OrderHandler.
func (h *CheckoutWorkflow) PaymentNotification(ctx restate.WorkflowSharedContext, noti payment.Notification) error {
	return h.gw.ResolvePaymentEvent(ctx, noti)
}

// CancelCheckout lets the buyer abort an in-flight checkout.
func (h *CheckoutWorkflow) CancelCheckout(ctx restate.WorkflowSharedContext, _ struct{}) error {
	return h.gw.Cancel(ctx)
}
