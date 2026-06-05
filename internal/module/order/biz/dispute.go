package orderbiz

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	restate "github.com/restatedev/sdk-go"

	"shopnexus-server/internal/infras/metrics"
	accountbiz "shopnexus-server/internal/module/account/biz"
	accountmodel "shopnexus-server/internal/module/account/model"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/validator"
)

// ListRefundDisputes powers the admin queue and buyer/seller visibility.
//   - Admin caller: returns every dispute (optionally filtered by status / refund_id).
//   - Non-admin caller: only disputes attached to refunds the caller owns
//     (buyer of the refund, or seller of the order).
func (b *disputeHandler) ListRefundDisputes(
	ctx restate.Context,
	params ListRefundDisputesParams,
) (paginate.PaginateResult[ordermodel.RefundDispute], error) {
	var zero paginate.PaginateResult[ordermodel.RefundDispute]
	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate list disputes: %w", err)
	}
	pagination := params.Params.Constrain()

	var (
		statusFilter orderdb.NullOrderDisputeStatus
		refundFilter uuid.NullUUID
		buyerFilter  uuid.NullUUID
		sellerFilter uuid.NullUUID
	)
	if params.Status != "" {
		statusFilter = orderdb.NullOrderDisputeStatus{OrderDisputeStatus: orderdb.OrderDisputeStatus(params.Status), Valid: true}
	}
	if params.RefundID.Valid {
		refundFilter = params.RefundID
	}
	if !params.Account.IsAdmin() {
		buyerFilter = uuid.NullUUID{UUID: params.Account.ID, Valid: true}
		sellerFilter = uuid.NullUUID{UUID: params.Account.ID, Valid: true}
	}

	rows, err := b.storage.Querier().ListRefundDisputes(ctx, orderdb.ListRefundDisputesParams{
		Status:         statusFilter,
		RefundID:       refundFilter,
		CallerBuyerID:  buyerFilter,
		CallerSellerID: sellerFilter,
		OffsetCount:    pagination.Offset().Int32,
		LimitCount:     pagination.Limit.Int32,
	})
	if err != nil {
		return zero, fmt.Errorf("list disputes: %w", err)
	}

	data := make([]ordermodel.RefundDispute, 0, len(rows))
	for _, r := range rows {
		data = append(data, mapRefundDispute(r))
	}
	return paginate.PaginateResult[ordermodel.RefundDispute]{PageParams: pagination, Data: data}, nil
}

// GetRefundDispute returns a single dispute. Caller must be admin OR the
// buyer/seller attached to the underlying refund.
func (b *disputeHandler) GetRefundDispute(
	ctx restate.Context,
	params GetRefundDisputeParams,
) (ordermodel.RefundDispute, error) {
	var zero ordermodel.RefundDispute
	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate get dispute: %w", err)
	}

	dispute, err := b.storage.Querier().GetRefundDispute(ctx, uuid.NullUUID{UUID: params.DisputeID, Valid: true})
	if err != nil {
		return zero, ordermodel.ErrDisputeNotFound
	}
	refund, err := b.storage.Querier().GetRefund(ctx, orderdb.GetRefundParams{
		ID: uuid.NullUUID{UUID: dispute.RefundID, Valid: true},
	})
	if err != nil {
		return zero, fmt.Errorf("get refund: %w", err)
	}
	order, err := b.storage.Querier().GetOrder(ctx, orderdb.GetOrderParams{
		ID: uuid.NullUUID{UUID: refund.OrderID, Valid: true},
	})
	if err != nil {
		return zero, fmt.Errorf("get order: %w", err)
	}
	if !params.Account.IsAdmin() &&
		params.Account.ID != refund.AccountID &&
		params.Account.ID != order.SellerID {
		return zero, ordermodel.ErrDisputeNotAuthorized
	}
	return mapRefundDispute(dispute), nil
}

// AdminUpholdDispute: admin sides with the seller. Refund flips to Rejected
// and a return-to-buyer transport is spawned so the goods go back. Payout to
// seller proceeds normally (escrow released by PayoutWorkflow on next tick).
func (b *disputeHandler) AdminUpholdDispute(
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

	pre, err := b.loadDisputeForResolution(ctx, params.DisputeID)
	if err != nil {
		return zero, err
	}

	// Create the return-to-buyer transport so the goods leave the seller's
	// hands. Mock-Success same as the forward leg.
	returnTransport, err := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderTransport, error) {
		t, e := b.storage.Querier().CreateDefaultTransport(rctx, orderdb.CreateDefaultTransportParams{
			Option: "default",
			Data:   json.RawMessage(`{"direction":"return","leg":"seller-to-buyer"}`),
		})
		if e != nil {
			return orderdb.OrderTransport{}, e
		}
		// TODO: remove mock when real transport provider is wired up.
		return b.storage.Querier().UpdateTransportStatusByID(rctx, orderdb.UpdateTransportStatusByIDParams{
			ID:     t.ID,
			Status: orderdb.NullOrderStatus{OrderStatus: orderdb.OrderStatusSuccess, Valid: true},
			Data:   json.RawMessage(`{"direction":"return","leg":"seller-to-buyer","mock":"auto-delivered"}`),
		})
	})
	if err != nil {
		return zero, fmt.Errorf("create return-to-buyer transport: %w", err)
	}

	updatedDispute, err := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderRefundDispute, error) {
		if _, e := b.storage.Querier().AdminUpholdDispute(rctx, orderdb.AdminUpholdDisputeParams{
			ID:                       pre.Refund.ID,
			ReturnToBuyerTransportID: null.IntFrom(returnTransport.ID),
			RejectionReason:          null.StringFrom(params.Note),
		}); e != nil {
			return orderdb.OrderRefundDispute{}, fmt.Errorf("uphold refund: %w", e)
		}
		return b.storage.Querier().ResolveRefundDispute(rctx, orderdb.ResolveRefundDisputeParams{
			ID:             pre.Dispute.ID,
			Status:         orderdb.OrderDisputeStatusSellerWins,
			ResolvedByID:   uuid.NullUUID{UUID: params.Account.ID, Valid: true},
			ResolutionNote: null.StringFrom(params.Note),
		})
	})
	if err != nil {
		return zero, fmt.Errorf("resolve dispute (uphold): %w", err)
	}

	restate.WorkflowSend(ctx, "RefundWorkflow", pre.Refund.ID.String(), "OnAdminDecision").Send(AdminDecisionSignal{Upheld: true})
	signalPayoutWorkflowOnRefundChanged(ctx, pre.Refund.OrderID)

	b.notifyDispute(ctx, pre, updatedDispute, "Dispute resolved",
		"The platform sided with the seller. The items are being shipped back to you; no refund will be issued.")

	return mapRefundDispute(updatedDispute), nil
}

// AdminDismissDispute: admin sides with the buyer. Refund flips to Accepted
// via the shared credit flow; the seller does not get paid.
func (b *disputeHandler) AdminDismissDispute(
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

	pre, err := b.loadDisputeForResolution(ctx, params.DisputeID)
	if err != nil {
		return zero, err
	}

	if _, err := b.executeRefundCredit(ctx, pre.Refund, params.Account.ID, refundCreditReasonAdminDismissed); err != nil {
		return zero, err
	}

	updatedDispute, err := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderRefundDispute, error) {
		return b.storage.Querier().ResolveRefundDispute(rctx, orderdb.ResolveRefundDisputeParams{
			ID:             pre.Dispute.ID,
			Status:         orderdb.OrderDisputeStatusBuyerWins,
			ResolvedByID:   uuid.NullUUID{UUID: params.Account.ID, Valid: true},
			ResolutionNote: null.StringFrom(params.Note),
		})
	})
	if err != nil {
		return zero, fmt.Errorf("resolve dispute (dismiss): %w", err)
	}

	restate.WorkflowSend(ctx, "RefundWorkflow", pre.Refund.ID.String(), "OnAdminDecision").Send(AdminDecisionSignal{Upheld: false})

	b.notifyDispute(ctx, pre, updatedDispute, "Dispute resolved",
		"The platform sided with the buyer. The refund has been credited.")

	return mapRefundDispute(updatedDispute), nil
}

type disputeContext struct {
	Dispute  orderdb.OrderRefundDispute
	Refund   orderdb.OrderRefund
	BuyerID  uuid.UUID
	SellerID uuid.UUID
}

func (b *disputeHandler) loadDisputeForResolution(
	ctx restate.Context,
	disputeID uuid.UUID,
) (disputeContext, error) {
	dispute, e := b.storage.Querier().GetRefundDispute(ctx, uuid.NullUUID{UUID: disputeID, Valid: true})
	if e != nil {
		return disputeContext{}, ordermodel.ErrDisputeNotFound
	}
	if dispute.Status != orderdb.OrderDisputeStatusOpen {
		return disputeContext{}, ordermodel.ErrDisputeRefundResolved
	}
	refund, e := b.storage.Querier().GetRefund(ctx, orderdb.GetRefundParams{
		ID: uuid.NullUUID{UUID: dispute.RefundID, Valid: true},
	})
	if e != nil {
		return disputeContext{}, fmt.Errorf("get refund: %w", e)
	}
	order, e := b.storage.Querier().GetOrder(ctx, orderdb.GetOrderParams{
		ID: uuid.NullUUID{UUID: refund.OrderID, Valid: true},
	})
	if e != nil {
		return disputeContext{}, fmt.Errorf("get order: %w", e)
	}
	return disputeContext{Dispute: dispute, Refund: refund, BuyerID: refund.AccountID, SellerID: order.SellerID}, nil
}

func (b *disputeHandler) notifyDispute(
	ctx restate.Context,
	pre disputeContext,
	dispute orderdb.OrderRefundDispute,
	title, content string,
) {
	meta, _ := json.Marshal(map[string]string{
		"refund_id":  dispute.RefundID.String(),
		"dispute_id": dispute.ID.String(),
		"outcome":    string(dispute.Status),
	})
	for _, accountID := range []uuid.UUID{pre.BuyerID, pre.SellerID} {
		restate.ServiceSend(ctx, "Account", "CreateNotification").Send(accountbiz.CreateNotificationParams{
			AccountID: accountID,
			Type:      accountmodel.NotiDisputeOpened,
			Channel:   accountmodel.ChannelInApp,
			Title:     title,
			Content:   content,
			Metadata:  meta,
		})
	}
}

type ListRefundDisputesParams struct {
	paginate.Params

	Account  accountmodel.AuthenticatedAccount
	RefundID uuid.NullUUID            `validate:"omitnil"`
	Status   ordermodel.DisputeStatus `validate:"omitempty,validateFn=Valid"`
}

type GetRefundDisputeParams struct {
	Account   accountmodel.AuthenticatedAccount
	DisputeID uuid.UUID `validate:"required"`
}

type AdminDisputeDecisionParams struct {
	Account   accountmodel.AuthenticatedAccount
	DisputeID uuid.UUID `json:"dispute_id"      validate:"required"`
	Note      string    `json:"resolution_note" validate:"required,min=1,max=2000"`
}
