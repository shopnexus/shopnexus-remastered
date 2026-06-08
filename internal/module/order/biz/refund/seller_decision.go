package refund

import (
	"fmt"

	"github.com/google/uuid"
	restate "github.com/restatedev/sdk-go"

	"shopnexus-server/internal/infras/metrics"
	accountmodel "shopnexus-server/internal/module/account/model"
	commonbiz "shopnexus-server/internal/module/common/biz"
	commondb "shopnexus-server/internal/module/common/db/sqlc"
	wfbase "shopnexus-server/internal/module/order/biz/workflow/base"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/shared/validator"
)

// SellerActionParams covers SellerApproveRefund (no body needed).
type SellerActionParams struct {
	Account  accountmodel.AuthenticatedAccount
	RefundID uuid.UUID `validate:"required"`
}

// SellerApproveRefund is the happy path: seller agrees with the refund after
// receiving the returned goods. Triggers the auto-credit flow.
func (b *RefundHandler) SellerApproveRefund(
	ctx restate.Context,
	params SellerActionParams,
) (ordermodel.Refund, error) {
	var zero ordermodel.Refund
	var err error
	defer metrics.TrackHandler("order", "SellerApproveRefund", &err)()

	if err = validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate seller approve: %w", err)
	}

	refund, err := b.loadAndAuthSeller(ctx, params.RefundID, params.Account.ID)
	if err != nil {
		return zero, fmt.Errorf("load seller refund: %w", err)
	}
	if !(ordermodel.Refund{OrderRefund: refund}).CanSellerDecide() {
		return zero, ordermodel.ErrRefundWrongStage
	}

	updated, err := b.ExecuteRefundCredit(ctx, refund, params.Account.ID, wfbase.RefundCreditReasonSellerApproved)
	if err != nil {
		return zero, fmt.Errorf("execute refund credit: %w", err)
	}

	if err = b.fulfillment.Send().OnSellerDecision(ctx, refund.OrderID, wfbase.SellerDecisionSignal{RefundID: refund.ID, Approved: true}); err != nil {
		return zero, fmt.Errorf("signal seller decision: %w", err)
	}
	if err = b.fulfillment.Send().OnRefundChanged(ctx, refund.OrderID); err != nil {
		return zero, fmt.Errorf("signal refund changed: %w", err)
	}

	if err = b.NotifyRefund(ctx, refund.AccountID, accountmodel.NotiRefundApproved,
		"Refund approved", "The seller approved your refund and your wallet has been credited.", updated); err != nil {
		return zero, fmt.Errorf("notify refund: %w", err)
	}

	refunds, err := b.HydrateRefunds(ctx, updated)
	if err != nil {
		return zero, fmt.Errorf("hydrate refund: %w", err)
	}
	return refunds[0], nil
}

type SellerDisputeParams struct {
	Account     accountmodel.AuthenticatedAccount
	RefundID    uuid.UUID   `validate:"required"`
	Reason      string      `validate:"required,min=1,max=1000"`
	ResourceIDs []uuid.UUID `validate:"required,min=1,max=20,dive"` // seller's evidence photos
}

// SellerDisputeRefund escalates the refund to admin. The seller provides a
// reason and evidence photos; the refund row flips to Disputed and a dispute
// row is opened.
func (b *RefundHandler) SellerDisputeRefund(
	ctx restate.Context,
	params SellerDisputeParams,
) (ordermodel.RefundDispute, error) {
	var zero ordermodel.RefundDispute
	var err error
	defer metrics.TrackHandler("order", "SellerDisputeRefund", &err)()

	if err = validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate seller dispute: %w", err)
	}

	refund, err := b.loadAndAuthSeller(ctx, params.RefundID, params.Account.ID)
	if err != nil {
		return zero, fmt.Errorf("load seller refund: %w", err)
	}
	if !(ordermodel.Refund{OrderRefund: refund}).CanSellerDecide() {
		return zero, ordermodel.ErrRefundWrongStage
	}

	dispute, err := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderRefundDispute, error) {
		if _, e := b.Storage.Querier().SellerDisputeRefund(rctx, refund.ID); e != nil {
			return orderdb.OrderRefundDispute{}, fmt.Errorf("dispute refund: %w", e)
		}
		return b.Storage.Querier().OpenRefundDispute(rctx, orderdb.OpenRefundDisputeParams{
			RefundID:  refund.ID,
			AccountID: params.Account.ID,
			Reason:    params.Reason,
		})
	})
	if err != nil {
		return zero, fmt.Errorf("open dispute: %w", err)
	}

	// Update the seller's evidence photos via the common resource system.
	resources, err := b.common.UpdateResources(ctx, commonbiz.UpdateResourcesParams{
		Account:     params.Account,
		RefType:     commondb.CommonResourceRefTypeRefundDispute,
		RefID:       dispute.ID,
		ResourceIDs: params.ResourceIDs,
	})
	if err != nil {
		return zero, fmt.Errorf("update dispute resources: %w", err)
	}

	if err = b.fulfillment.Send().OnSellerDecision(ctx, refund.OrderID, wfbase.SellerDecisionSignal{RefundID: refund.ID, Approved: false}); err != nil {
		return zero, fmt.Errorf("signal seller decision: %w", err)
	}

	if err = b.NotifyRefund(ctx, refund.AccountID, accountmodel.NotiDisputeOpened,
		"Refund disputed", "The seller has disputed your refund. Our team will review the case.", refund); err != nil {
		return zero, fmt.Errorf("notify refund: %w", err)
	}

	return ordermodel.RefundDispute{OrderRefundDispute: dispute, Resources: resources}, nil
}

// loadAndAuthSeller fetches the refund and verifies the caller is the order's
// seller. Shared between SellerApproveRefund + SellerDisputeRefund.
func (b *RefundHandler) loadAndAuthSeller(
	ctx restate.Context,
	refundID uuid.UUID,
	callerID uuid.UUID,
) (orderdb.OrderRefund, error) {
	refund, err := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderRefund, error) {
		return b.Storage.Querier().GetRefund(rctx, orderdb.GetRefundParams{
			ID: uuid.NullUUID{UUID: refundID, Valid: true},
		})
	})
	if err != nil {
		return orderdb.OrderRefund{}, fmt.Errorf("get refund: %w", err)
	}
	order, err := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderOrder, error) {
		return b.Storage.Querier().GetOrder(rctx, orderdb.GetOrderParams{
			ID: uuid.NullUUID{UUID: refund.OrderID, Valid: true},
		})
	})
	if err != nil {
		return orderdb.OrderRefund{}, fmt.Errorf("get order: %w", err)
	}
	if order.SellerID != callerID {
		return orderdb.OrderRefund{}, ordermodel.ErrItemNotOwnedBySeller
	}
	return refund, nil
}
