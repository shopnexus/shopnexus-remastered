package fullfilment

import (
	"fmt"

	restate "github.com/restatedev/sdk-go"

	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/provider/payment"
)

// --- Payment-gate handlers: delegate to the shared gateway engine. ---

// WaitPaymentURL blocks until Run resolves the FIRST attempt's URL. Used by the
// sync /seller/pending/confirm HTTP handler.
func (h *FulfillmentWorkflow) WaitPaymentURL(ctx restate.WorkflowSharedContext, _ struct{}) (string, error) {
	return h.gw.WaitFirstURL(ctx)
}

// RequestNewPaymentURL is the multi-attempt entry point for a dead/expired tx.
func (h *FulfillmentWorkflow) RequestNewPaymentURL(ctx restate.WorkflowSharedContext, _ struct{}) (string, error) {
	return h.gw.RequestNewURL(ctx, ordermodel.ErrConfirmExpired)
}

// PaymentNotification is called by the payment provider via OrderHandler.OnPaymentResult.
func (h *FulfillmentWorkflow) PaymentNotification(ctx restate.WorkflowSharedContext, noti payment.Notification) error {
	return h.gw.ResolvePaymentEvent(ctx, noti)
}

// CancelConfirm lets the seller abort an in-flight confirm.
func (h *FulfillmentWorkflow) CancelConfirm(ctx restate.WorkflowSharedContext, _ struct{}) error {
	return h.gw.Cancel(ctx)
}

// --- Refund / dispute signals. ---

// OnRefundChanged is signalled by the refund/dispute handlers every time a
// refund row for this order transitions state. It resolves the promise the
// escrow loop is currently blocked on, identified by the iteration counter
// persisted in K/V state.
func (h *FulfillmentWorkflow) OnRefundChanged(ctx restate.WorkflowSharedContext, _ struct{}) error {
	iter, err := restate.Get[int](ctx, "refund_iter")
	if err != nil {
		return fmt.Errorf("read refund_iter state: %w", err)
	}
	// iter == 0 means the escrow loop hasn't started waiting yet — the next
	// iteration's snapshot picks up the change anyway, so no-op.
	if iter == 0 {
		return nil
	}
	return restate.Promise[any](ctx, fmt.Sprintf("refund_changed_%d", iter)).Resolve(nil)
}

// OnBuyerWithdrew is signalled by WithdrawBuyerRefund to abort the refund's
// shipping phase.
func (h *FulfillmentWorkflow) OnBuyerWithdrew(ctx restate.WorkflowSharedContext, sig ordermodel.RefundSignal) error {
	return restate.Promise[any](ctx, "withdrawn_"+sig.RefundID.String()).Resolve(nil)
}

// OnSellerDecision is signalled by SellerApproveRefund and SellerDisputeRefund.
func (h *FulfillmentWorkflow) OnSellerDecision(ctx restate.WorkflowSharedContext, sig ordermodel.SellerDecisionSignal) error {
	return restate.Promise[ordermodel.SellerDecisionSignal](ctx, "seller_decision_"+sig.RefundID.String()).Resolve(sig)
}

// OnAdminDecision is signalled by AdminUpholdDispute and AdminDismissDispute.
func (h *FulfillmentWorkflow) OnAdminDecision(ctx restate.WorkflowSharedContext, sig ordermodel.AdminDecisionSignal) error {
	return restate.Promise[ordermodel.AdminDecisionSignal](ctx, "admin_decision_"+sig.RefundID.String()).Resolve(sig)
}

// OnTransportDelivered is signalled by the real transport webhook when a
// buyer's return shipment is physically delivered, releasing the escrow loop's
// phase-1 wait.
func (h *FulfillmentWorkflow) OnTransportDelivered(ctx restate.WorkflowSharedContext, sig ordermodel.TransportDeliveredSignal) error {
	return restate.Promise[any](ctx, returnDeliveredKey(sig.RefundID)).Resolve(nil)
}
