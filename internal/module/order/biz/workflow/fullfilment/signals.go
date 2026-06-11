package fullfilment

import (
	"fmt"

	restate "github.com/restatedev/sdk-go"

	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/provider/payment"
)

// GetPaymentURL resolves a usable gateway redirect URL from the journaled gate
// state: reuse the live attempt, advance to a fresh one, or return the terminal
// outcome. Serves both the initial confirm submit and its retry endpoint.
func (h *FulfillmentWorkflow) GetPaymentURL(ctx restate.WorkflowSharedContext, _ struct{}) (string, error) {
	return h.gw.GetPaymentURL(ctx, ordermodel.ErrConfirmExpired, ordermodel.ErrConfirmCancelled)
}

// PaymentNotification is called by the payment provider via OrderHandler.
func (h *FulfillmentWorkflow) PaymentNotification(ctx restate.WorkflowSharedContext, noti payment.Notification) error {
	return h.gw.ResolvePaymentEvent(ctx, noti)
}

// CancelConfirm lets the seller abort an in-flight confirm.
func (h *FulfillmentWorkflow) CancelConfirm(ctx restate.WorkflowSharedContext, _ struct{}) error {
	return h.gw.Cancel(ctx)
}

// OnRefundChanged resolves the per-iteration promise the escrow loop is blocked on.
func (h *FulfillmentWorkflow) OnRefundChanged(ctx restate.WorkflowSharedContext, _ struct{}) error {
	iter, err := restate.Get[int](ctx, "refund_iter")
	if err != nil {
		return fmt.Errorf("read refund_iter state: %w", err)
	}
	// iter == 0: loop hasn't started waiting yet; next snapshot picks up the change.
	if iter == 0 {
		return nil
	}
	return restate.Promise[any](ctx, fmt.Sprintf("refund_changed_%d", iter)).Resolve(nil)
}

// OnBuyerWithdrew aborts the refund's shipping phase.
func (h *FulfillmentWorkflow) OnBuyerWithdrew(ctx restate.WorkflowSharedContext, sig ordermodel.RefundSignal) error {
	return restate.Promise[any](ctx, "withdrawn_"+sig.RefundID.String()).Resolve(nil)
}

// OnSellerDecision is called by SellerApproveRefund and SellerDisputeRefund.
func (h *FulfillmentWorkflow) OnSellerDecision(ctx restate.WorkflowSharedContext, sig ordermodel.SellerDecisionSignal) error {
	return restate.Promise[ordermodel.SellerDecisionSignal](ctx, "seller_decision_"+sig.RefundID.String()).Resolve(sig)
}

// OnAdminDecision is called by AdminUpholdDispute and AdminDismissDispute.
func (h *FulfillmentWorkflow) OnAdminDecision(ctx restate.WorkflowSharedContext, sig ordermodel.AdminDecisionSignal) error {
	return restate.Promise[ordermodel.AdminDecisionSignal](ctx, "admin_decision_"+sig.RefundID.String()).Resolve(sig)
}

// OnTransportDelivered fires when a buyer's return shipment is physically delivered.
func (h *FulfillmentWorkflow) OnTransportDelivered(ctx restate.WorkflowSharedContext, sig ordermodel.TransportDeliveredSignal) error {
	return restate.Promise[any](ctx, returnDeliveredKey(sig.RefundID)).Resolve(nil)
}
