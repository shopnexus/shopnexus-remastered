package fullfilment

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	restate "github.com/restatedev/sdk-go"

	accountmodel "shopnexus-server/internal/module/account/model"
	ordermodel "shopnexus-server/internal/module/order/model"
	orderrepo "shopnexus-server/internal/module/order/repo"
)

// autoAcceptRefund fires on review-window or shipping-timeout expiry with no seller decision.
func (r *fulfillmentRun) autoAcceptRefund(refundID uuid.UUID) error {
	ctx := r.ctx
	refund, err := restate.Run(ctx, func(rctx restate.RunContext) (ordermodel.Refund, error) {
		return r.Storage.Querier().GetRefund(rctx, refundID)
	})
	if err != nil {
		return fmt.Errorf("get refund: %w", err)
	}
	// A manual decision may have already closed the refund.
	if !refund.CanSellerDecide() {
		return nil
	}

	// ExecuteRefundCredit self-journals its phases, so call it at the workflow's restate.Context directly.
	updated, err := r.refund.ExecuteRefundCredit(ctx, refund, refund.AccountID, ordermodel.RefundCreditReasonAutoAccepted)
	if err != nil {
		return fmt.Errorf("execute refund credit: %w", err)
	}
	if err = r.NotifyRefund(
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

// markRefundDelivered flips a Shipping refund to AwaitingSellerReview on return delivery.
func (r *fulfillmentRun) markRefundDelivered(refundID uuid.UUID) error {
	ctx := r.ctx
	deadline := time.Now().Add(sellerReviewWindow)
	updated, err := restate.Run(ctx, func(rctx restate.RunContext) (ordermodel.Refund, error) {
		return r.Storage.Querier().MarkRefundDelivered(rctx, orderrepo.MarkRefundDeliveredParams{
			ID:             refundID,
			ReviewDeadline: null.TimeFrom(deadline),
		})
	})
	if err != nil {
		return fmt.Errorf("mark delivered: %w", err)
	}

	order, _ := restate.Run(ctx, func(rctx restate.RunContext) (ordermodel.Order, error) {
		return r.Storage.Querier().GetOrder(rctx, updated.OrderID)
	})
	if err = r.NotifyRefund(ctx, order.SellerID, accountmodel.NotiRefundRequested,
		"Return delivered", "The buyer's return shipment has arrived. Please review within 3 days.", updated); err != nil {
		return fmt.Errorf("notify refund: %w", err)
	}

	return nil
}
