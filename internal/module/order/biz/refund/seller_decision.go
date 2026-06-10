package refund

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"shopnexus-server/internal/infras/metrics"
	accountmodel "shopnexus-server/internal/module/account/model"
	commonbiz "shopnexus-server/internal/module/common/biz"
	commondb "shopnexus-server/internal/module/common/db/sqlc"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/shared/validator"

	restate "github.com/restatedev/sdk-go"
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

	// decision: load + authorize the refund and confirm the seller can still decide.
	refund, err := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderRefund, error) {
		r, e := b.loadAndAuthSeller(rctx, params.RefundID, params.Account.ID)
		if e != nil {
			return orderdb.OrderRefund{}, fmt.Errorf("load seller refund: %w", e)
		}
		if !(ordermodel.Refund{OrderRefund: r}).CanSellerDecide() {
			return orderdb.OrderRefund{}, ordermodel.ErrRefundWrongStage
		}
		return r, nil
	})
	if err != nil {
		return zero, err
	}

	// execution: run the credit flow. ExecuteRefundCredit self-journals its phases.
	updated, err := b.ExecuteRefundCredit(ctx, refund, params.Account.ID, ordermodel.RefundCreditReasonSellerApproved)
	if err != nil {
		return zero, fmt.Errorf("execute refund credit: %w", err)
	}

	// tail: signal the workflow + notify the buyer. Each Send self-journals.
	if err = b.fulfillment.Send().OnSellerDecision(ctx, refund.OrderID, ordermodel.SellerDecisionSignal{RefundID: refund.ID, Approved: true}); err != nil {
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

	// decision: load + authorize the refund and confirm the seller can still decide.
	refund, err := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderRefund, error) {
		r, e := b.loadAndAuthSeller(rctx, params.RefundID, params.Account.ID)
		if e != nil {
			return orderdb.OrderRefund{}, fmt.Errorf("load seller refund: %w", e)
		}
		if !(ordermodel.Refund{OrderRefund: r}).CanSellerDecide() {
			return orderdb.OrderRefund{}, ordermodel.ErrRefundWrongStage
		}
		return r, nil
	})
	if err != nil {
		return zero, err
	}

	// execution: flip the refund to Disputed and open the dispute row.
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

	// tail: update the seller's evidence photos via the common resource system.
	resources, err := b.common.Call().UpdateResources(ctx, commonbiz.UpdateResourcesParams{
		Account:     params.Account,
		RefType:     commondb.CommonResourceRefTypeRefundDispute,
		RefID:       dispute.ID,
		ResourceIDs: params.ResourceIDs,
	})
	if err != nil {
		return zero, fmt.Errorf("update dispute resources: %w", err)
	}

	if err = b.fulfillment.Send().OnSellerDecision(ctx, refund.OrderID, ordermodel.SellerDecisionSignal{RefundID: refund.ID, Approved: false}); err != nil {
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
	ctx context.Context,
	refundID uuid.UUID,
	callerID uuid.UUID,
) (orderdb.OrderRefund, error) {
	refund, err := b.Storage.Querier().GetRefund(ctx, orderdb.GetRefundParams{
		ID: uuid.NullUUID{UUID: refundID, Valid: true},
	})
	if err != nil {
		return orderdb.OrderRefund{}, fmt.Errorf("get refund: %w", err)
	}
	order, err := b.Storage.Querier().GetOrder(ctx, orderdb.GetOrderParams{
		ID: uuid.NullUUID{UUID: refund.OrderID, Valid: true},
	})
	if err != nil {
		return orderdb.OrderRefund{}, fmt.Errorf("get order: %w", err)
	}
	if order.SellerID != callerID {
		return orderdb.OrderRefund{}, ordermodel.ErrItemNotOwnedBySeller
	}
	return refund, nil
}
