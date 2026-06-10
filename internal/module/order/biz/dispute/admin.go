package dispute

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	restate "github.com/restatedev/sdk-go"

	"shopnexus-server/internal/infras/metrics"
	accountmodel "shopnexus-server/internal/module/account/model"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/shared/validator"
)

// AdminUpholdDispute: admin sides with the seller. Refund flips to Rejected
// and a return-to-buyer transport is spawned so the goods go back. Payout to
// seller proceeds normally (escrow released by the order's FulfillmentWorkflow on next tick).
func (b *DisputeHandler) AdminUpholdDispute(
	ctx restate.Context,
	params AdminDisputeDecisionParams,
) (ordermodel.RefundDispute, error) {
	var zero ordermodel.RefundDispute
	var err error
	defer metrics.TrackHandler("order", "AdminUpholdDispute", &err)()

	if err = validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate admin uphold: %w", err)
	}
	if !params.Account.IsAdmin() {
		return zero, ordermodel.ErrAdminRequired
	}

	// decision: load the open dispute + its refund/order.
	pre, err := restate.Run(ctx, func(rctx restate.RunContext) (disputeContext, error) {
		return b.loadDisputeForResolution(rctx, params.DisputeID)
	})
	if err != nil {
		return zero, fmt.Errorf("load dispute: %w", err)
	}

	// execution: spawn the return-to-buyer transport (mock-Success same as the
	// forward leg), uphold the refund and resolve the dispute as seller-wins.
	updatedDispute, err := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderRefundDispute, error) {
		t, e := b.Storage.Querier().CreateDefaultTransport(rctx, orderdb.CreateDefaultTransportParams{
			Option: "default",
			Data:   json.RawMessage(`{"direction":"return","leg":"seller-to-buyer"}`),
		})
		if e != nil {
			return orderdb.OrderRefundDispute{}, fmt.Errorf("create return-to-buyer transport: %w", e)
		}
		// TODO: remove mock when real transport provider is wired up.
		returnTransport, e := b.Storage.Querier().UpdateTransportStatusByID(rctx, orderdb.UpdateTransportStatusByIDParams{
			ID:     t.ID,
			Status: orderdb.NullOrderStatus{OrderStatus: orderdb.OrderStatusSuccess, Valid: true},
			Data:   json.RawMessage(`{"direction":"return","leg":"seller-to-buyer","mock":"auto-delivered"}`),
		})
		if e != nil {
			return orderdb.OrderRefundDispute{}, fmt.Errorf("create return-to-buyer transport: %w", e)
		}

		if _, e := b.Storage.Querier().AdminUpholdDispute(rctx, orderdb.AdminUpholdDisputeParams{
			ID:                       pre.Refund.ID,
			ReturnToBuyerTransportID: null.IntFrom(returnTransport.ID),
			RejectionReason:          null.StringFrom(params.Note),
		}); e != nil {
			return orderdb.OrderRefundDispute{}, fmt.Errorf("uphold refund: %w", e)
		}
		return b.Storage.Querier().ResolveRefundDispute(rctx, orderdb.ResolveRefundDisputeParams{
			ID:             pre.Dispute.ID,
			Status:         orderdb.OrderDisputeStatusSellerWins,
			ResolvedByID:   uuid.NullUUID{UUID: params.Account.ID, Valid: true},
			ResolutionNote: null.StringFrom(params.Note),
		})
	})
	if err != nil {
		return zero, fmt.Errorf("resolve dispute (uphold): %w", err)
	}

	// tail: signal the workflow + notify both parties. Each Send self-journals.
	if err = b.fulfillment.Send().OnAdminDecision(ctx, pre.Refund.OrderID, ordermodel.AdminDecisionSignal{RefundID: pre.Refund.ID, Upheld: true}); err != nil {
		return zero, fmt.Errorf("signal admin decision: %w", err)
	}
	if err = b.fulfillment.Send().OnRefundChanged(ctx, pre.Refund.OrderID); err != nil {
		return zero, fmt.Errorf("signal refund changed: %w", err)
	}

	if err = b.NotifyDispute(ctx, pre.BuyerID, pre.SellerID, updatedDispute, "Dispute resolved",
		"The platform sided with the seller. The items are being shipped back to you; no refund will be issued."); err != nil {
		return zero, err
	}

	disputes, err := b.HydrateRefundDisputes(ctx, updatedDispute)
	if err != nil {
		return zero, fmt.Errorf("hydrate dispute: %w", err)
	}

	return disputes[0], nil
}

// AdminDismissDispute: admin sides with the buyer. Refund flips to Accepted
// via the shared credit flow; the seller does not get paid.
func (b *DisputeHandler) AdminDismissDispute(
	ctx restate.Context,
	params AdminDisputeDecisionParams,
) (ordermodel.RefundDispute, error) {
	var zero ordermodel.RefundDispute
	var err error
	defer metrics.TrackHandler("order", "AdminDismissDispute", &err)()

	if err = validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate admin dismiss: %w", err)
	}
	if !params.Account.IsAdmin() {
		return zero, ordermodel.ErrAdminRequired
	}

	// decision: load the open dispute + its refund/order.
	pre, err := restate.Run(ctx, func(rctx restate.RunContext) (disputeContext, error) {
		return b.loadDisputeForResolution(rctx, params.DisputeID)
	})
	if err != nil {
		return zero, fmt.Errorf("load dispute: %w", err)
	}

	// execution: run the credit flow (self-journals), then resolve the dispute
	// as buyer-wins.
	if _, err := b.refund.ExecuteRefundCredit(
		ctx,
		pre.Refund,
		params.Account.ID,
		ordermodel.RefundCreditReasonAdminDismissed,
	); err != nil {
		return zero, fmt.Errorf("execute refund credit: %w", err)
	}

	updatedDispute, err := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderRefundDispute, error) {
		return b.Storage.Querier().ResolveRefundDispute(rctx, orderdb.ResolveRefundDisputeParams{
			ID:             pre.Dispute.ID,
			Status:         orderdb.OrderDisputeStatusBuyerWins,
			ResolvedByID:   uuid.NullUUID{UUID: params.Account.ID, Valid: true},
			ResolutionNote: null.StringFrom(params.Note),
		})
	})
	if err != nil {
		return zero, fmt.Errorf("resolve dispute (dismiss): %w", err)
	}

	// tail: signal the workflow + notify both parties. Each Send self-journals.
	if err = b.fulfillment.Send().OnAdminDecision(ctx, pre.Refund.OrderID, ordermodel.AdminDecisionSignal{RefundID: pre.Refund.ID, Upheld: false}); err != nil {
		return zero, fmt.Errorf("signal admin decision: %w", err)
	}
	if err = b.fulfillment.Send().OnRefundChanged(ctx, pre.Refund.OrderID); err != nil {
		return zero, fmt.Errorf("signal refund changed: %w", err)
	}

	if err = b.NotifyDispute(ctx, pre.BuyerID, pre.SellerID, updatedDispute, "Dispute resolved",
		"The platform sided with the buyer. The refund has been credited."); err != nil {
		return zero, err
	}

	disputes, err := b.HydrateRefundDisputes(ctx, updatedDispute)
	if err != nil {
		return zero, fmt.Errorf("hydrate dispute: %w", err)
	}
	return disputes[0], nil
}

type disputeContext struct {
	Dispute  orderdb.OrderRefundDispute
	Refund   orderdb.OrderRefund
	BuyerID  uuid.UUID
	SellerID uuid.UUID
}

func (b *DisputeHandler) loadDisputeForResolution(
	ctx context.Context,
	disputeID uuid.UUID,
) (disputeContext, error) {
	dispute, e := b.Storage.Querier().GetRefundDispute(ctx, uuid.NullUUID{UUID: disputeID, Valid: true})
	if e != nil {
		return disputeContext{}, ordermodel.ErrDisputeNotFound
	}
	if dispute.Status != orderdb.OrderDisputeStatusOpen {
		return disputeContext{}, ordermodel.ErrDisputeRefundResolved
	}
	refund, e := b.Storage.Querier().GetRefund(ctx, orderdb.GetRefundParams{
		ID: uuid.NullUUID{UUID: dispute.RefundID, Valid: true},
	})
	if e != nil {
		return disputeContext{}, fmt.Errorf("get refund: %w", e)
	}
	order, e := b.Storage.Querier().GetOrder(ctx, orderdb.GetOrderParams{
		ID: uuid.NullUUID{UUID: refund.OrderID, Valid: true},
	})
	if e != nil {
		return disputeContext{}, fmt.Errorf("get order: %w", e)
	}
	return disputeContext{Dispute: dispute, Refund: refund, BuyerID: refund.AccountID, SellerID: order.SellerID}, nil
}

type AdminDisputeDecisionParams struct {
	Account   accountmodel.AuthenticatedAccount
	DisputeID uuid.UUID `validate:"required"`
	Note      string    `validate:"required,min=1,max=2000"`
}
