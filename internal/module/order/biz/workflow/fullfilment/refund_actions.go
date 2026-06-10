package fullfilment

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	restate "github.com/restatedev/sdk-go"

	accountmodel "shopnexus-server/internal/module/account/model"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
)

// Refund v2 timers, consumed only by the escrow loop + the actions below.
const (
	// sellerReviewWindow is how long the seller has from physical delivery to
	// decide accept/dispute before the refund auto-accepts in the buyer's favor.
	sellerReviewWindow = 3 * 24 * time.Hour
	// forwardTransportTimeout caps how long we wait for the return-transport
	// webhook to fire. After this, the refund auto-accepts (defends against
	// lost packages — the platform eats the loss rather than stranding the buyer).
	forwardTransportTimeout = 14 * 24 * time.Hour
)

// autoAcceptRefund fires when a refund timer expires with no seller decision
// (review window or shipping timeout). Same credit flow as SellerApproveRefund.
func (h *FulfillmentWorkflow) autoAcceptRefund(ctx restate.WorkflowContext, refundID uuid.UUID) error {
	refund, err := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderRefund, error) {
		return h.Storage.Querier().GetRefund(rctx, orderdb.GetRefundParams{
			ID: uuid.NullUUID{UUID: refundID, Valid: true},
		})
	})
	if err != nil {
		return fmt.Errorf("get refund: %w", err)
	}
	// Idempotent: a manual seller decision may have already closed the refund.
	// Treat that as success.
	if !(ordermodel.Refund{OrderRefund: refund}).CanSellerDecide() {
		return nil
	}

	updated, err := h.refund.ExecuteRefundCredit(ctx, refund, refund.AccountID, ordermodel.RefundCreditReasonAutoAccepted)
	if err != nil {
		return fmt.Errorf("execute refund credit: %w", err)
	}
	if err = h.NotifyRefund(
		ctx,
		refund.AccountID,
		accountmodel.NotiRefundApproved,
		"Refund auto-approved",
		"The seller did not respond in time, so your refund has been auto-approved and credited.",
		updated,
	); err != nil {
		return fmt.Errorf("notify refund: %w", err)
	}
	return nil
}

// markRefundDelivered flips a Shipping refund to AwaitingSellerReview when the
// return transport reports delivery, and arms the seller review window.
func (h *FulfillmentWorkflow) markRefundDelivered(ctx restate.WorkflowContext, refundID uuid.UUID) error {
	deadline := time.Now().Add(sellerReviewWindow)
	updated, err := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderRefund, error) {
		return h.Storage.Querier().MarkRefundDelivered(rctx, orderdb.MarkRefundDeliveredParams{
			ID:             refundID,
			ReviewDeadline: null.TimeFrom(deadline),
		})
	})
	if err != nil {
		return fmt.Errorf("mark delivered: %w", err)
	}

	// Get order to notify the seller their action is now expected.
	order, _ := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderOrder, error) {
		return h.Storage.Querier().GetOrder(rctx, orderdb.GetOrderParams{
			ID: uuid.NullUUID{UUID: updated.OrderID, Valid: true},
		})
	})
	if err = h.NotifyRefund(ctx, order.SellerID, accountmodel.NotiRefundRequested,
		"Return delivered", "The buyer's return shipment has arrived. Please review within 3 days.", updated); err != nil {
		return fmt.Errorf("notify refund: %w", err)
	}

	return nil
}
