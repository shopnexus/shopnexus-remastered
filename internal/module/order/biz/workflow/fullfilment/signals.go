package fullfilment

import (
	"fmt"

	restate "github.com/restatedev/sdk-go"

	"shopnexus-server/internal/module/order/biz/workflow/base"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/provider/payment"
)

// WaitPaymentURL blocks until Run resolves the FIRST attempt's URL. Used by
// the sync /seller/pending/confirm HTTP handler. Subsequent retries go
// through RequestNewPaymentURL.
func (h *FulfillmentWorkflow) WaitPaymentURL(
	ctx restate.WorkflowSharedContext,
	_ struct{},
) (string, error) {
	return restate.Promise[string](ctx, "payment_url_1").Result()
}

// RequestNewPaymentURL is the multi-attempt entry point. Caller has already
// verified the latest gateway tx is Failed/expired. We resolve the current
// attempt's retry promise (idempotent) so Run advances to attempt+1, then
// block on the new URL.
func (h *FulfillmentWorkflow) RequestNewPaymentURL(
	ctx restate.WorkflowSharedContext,
	_ struct{},
) (string, error) {
	attempt, err := restate.Get[int](ctx, "payment_attempt")
	if err != nil {
		return "", fmt.Errorf("read payment_attempt state: %w", err)
	}
	if attempt < 1 {
		return "", ordermodel.ErrConfirmExpired
	}
	_ = restate.Promise[struct{}](ctx, fmt.Sprintf("retry_%d", attempt)).Resolve(struct{}{})
	return restate.Promise[string](ctx, fmt.Sprintf("payment_url_%d", attempt+1)).Result()
}

// PaymentNotification is called by the payment provider via OrderHandler.
// OnPaymentResult. The webhook's RefID is the gateway tx UUID — we key the
// promise by it so late webhooks for already-Failed prior attempts are
// silently no-ops.
func (h *FulfillmentWorkflow) PaymentNotification(
	ctx restate.WorkflowSharedContext,
	noti payment.Notification,
) error {
	return restate.Promise[payment.Notification](ctx, "payment_event_"+noti.RefID).Resolve(noti)
}

// CancelConfirm lets the seller abort an in-flight confirm.
func (h *FulfillmentWorkflow) CancelConfirm(
	ctx restate.WorkflowSharedContext,
	_ struct{},
) error {
	return restate.Promise[struct{}](ctx, "user_cancel").Resolve(struct{}{})
}

// OnRefundChanged is signalled by the refund/dispute handlers every time a
// refund row for this order transitions state. It resolves the promise the
// escrow loop is currently blocked on, identified by the iteration counter
// persisted in K/V state.
func (h *FulfillmentWorkflow) OnRefundChanged(
	ctx restate.WorkflowSharedContext,
	_ struct{},
) error {
	iter, err := restate.Get[int](ctx, "refund_iter")
	if err != nil {
		return fmt.Errorf("read refund_iter state: %w", err)
	}
	// iter == 0 means the escrow loop hasn't started waiting yet — the next
	// iteration's snapshot will pick up the change anyway, so no-op.
	if iter == 0 {
		return nil
	}
	return restate.Promise[any](ctx, fmt.Sprintf("refund_changed_%d", iter)).Resolve(nil)
}

// OnBuyerWithdrew is signalled by WithdrawBuyerRefund to abort the refund's
// shipping phase.
func (h *FulfillmentWorkflow) OnBuyerWithdrew(
	ctx restate.WorkflowSharedContext,
	sig base.RefundSignal,
) error {
	return restate.Promise[any](ctx, "withdrawn_"+sig.RefundID.String()).Resolve(nil)
}

// OnSellerDecision is signalled by SellerApproveRefund and SellerDisputeRefund.
func (h *FulfillmentWorkflow) OnSellerDecision(
	ctx restate.WorkflowSharedContext,
	sig base.SellerDecisionSignal,
) error {
	return restate.Promise[base.SellerDecisionSignal](ctx, "seller_decision_"+sig.RefundID.String()).Resolve(sig)
}

// OnAdminDecision is signalled by AdminUpholdDispute and AdminDismissDispute.
func (h *FulfillmentWorkflow) OnAdminDecision(
	ctx restate.WorkflowSharedContext,
	sig base.AdminDecisionSignal,
) error {
	return restate.Promise[base.AdminDecisionSignal](ctx, "admin_decision_"+sig.RefundID.String()).Resolve(sig)
}
