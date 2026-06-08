package refund

import (
	"fmt"

	"github.com/google/uuid"
	restate "github.com/restatedev/sdk-go"

	"shopnexus-server/internal/infras/metrics"
	accountmodel "shopnexus-server/internal/module/account/model"
	wfbase "shopnexus-server/internal/module/order/biz/workflow/base"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/shared/validator"
)

// WithdrawBuyerRefundParams covers the buyer-initiated cancel. Caller must be
// the refund's buyer and the refund must be in Shipping.
type WithdrawBuyerRefundParams struct {
	Account  accountmodel.AuthenticatedAccount
	RefundID uuid.UUID `validate:"required"`
}

// WithdrawBuyerRefund cancels a Shipping refund at the buyer's request. Only
// the buyer who created the refund can withdraw, and only while the goods are
// still in transit — once the seller has the items (AwaitingSellerReview),
// withdraw is blocked. The refund row flips to Cancelled (terminal), the
// payout watcher resumes the seller's escrow, and the workflow exits via the
// "withdrawn" promise.
func (b *RefundHandler) WithdrawBuyerRefund(
	ctx restate.Context,
	params WithdrawBuyerRefundParams,
) (ordermodel.Refund, error) {
	var zero ordermodel.Refund
	var err error
	defer metrics.TrackHandler("order", "WithdrawBuyerRefund", &err)()

	if err = validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate withdraw refund: %w", err)
	}

	// SQL guards on status='Shipping' AND account_id=caller, so a row update of
	// zero means the refund is in a non-withdrawable state OR the caller is not
	// the buyer. We translate that to ErrRefundNotWithdrawable rather than
	// leaking ErrNoRows.
	refund, err := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderRefund, error) {
		return b.Storage.Querier().WithdrawBuyerRefund(rctx, orderdb.WithdrawBuyerRefundParams{
			ID:        params.RefundID,
			AccountID: params.Account.ID,
		})
	})
	if err != nil {
		return zero, ordermodel.ErrRefundNotWithdrawable
	}

	// Tell the workflow to exit the refund phase early; escrow resumes.
	if err = b.fulfillment.Send().OnBuyerWithdrew(ctx, refund.OrderID, wfbase.RefundSignal{RefundID: refund.ID}); err != nil {
		return zero, fmt.Errorf("signal buyer withdrew: %w", err)
	}
	if err = b.fulfillment.Send().OnRefundChanged(ctx, refund.OrderID); err != nil {
		return zero, fmt.Errorf("signal refund changed: %w", err)
	}

	// Notify seller (was waiting on the inbound return) so their UI clears.
	order, err := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderOrder, error) {
		return b.Storage.Querier().GetOrder(rctx, orderdb.GetOrderParams{
			ID: uuid.NullUUID{UUID: refund.OrderID, Valid: true},
		})
	})
	if err == nil {
		if err = b.NotifyRefund(ctx, order.SellerID, accountmodel.NotiRefundRequested,
			"Refund withdrawn", "The buyer withdrew their refund request before the return arrived.", refund); err != nil {
			return zero, fmt.Errorf("notify refund: %w", err)
		}
	}

	refunds, err := b.HydrateRefunds(ctx, refund)
	if err != nil {
		return zero, fmt.Errorf("hydrate refund: %w", err)
	}
	return refunds[0], nil
}
