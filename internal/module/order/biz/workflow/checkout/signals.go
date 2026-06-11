package checkout

import (
	restate "github.com/restatedev/sdk-go"

	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/provider/payment"
)

// GetPaymentURL resolves a usable gateway redirect URL from the journaled gate
// state: reuse the live attempt, advance to a fresh one, or return the terminal
// outcome. Serves both the initial /buyer/checkout submit and its retry endpoint.
func (h *CheckoutWorkflow) GetPaymentURL(ctx restate.WorkflowSharedContext, _ struct{}) (string, error) {
	return h.gw.GetPaymentURL(ctx, ordermodel.ErrCheckoutExpired, ordermodel.ErrCheckoutCancelled)
}

// PaymentNotification is called by the payment provider via OrderHandler.
func (h *CheckoutWorkflow) PaymentNotification(ctx restate.WorkflowSharedContext, noti payment.Notification) error {
	return h.gw.ResolvePaymentEvent(ctx, noti)
}

// CancelCheckout lets the buyer abort an in-flight checkout.
func (h *CheckoutWorkflow) CancelCheckout(ctx restate.WorkflowSharedContext, _ struct{}) error {
	return h.gw.Cancel(ctx)
}
