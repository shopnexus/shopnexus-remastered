package refund

import (
	"encoding/json"
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

type CreateBuyerRefundParams struct {
	Account      accountmodel.AuthenticatedAccount
	OrderID      uuid.UUID   `validate:"required"`
	Reason       string      `validate:"required,min=1,max=1000"`
	ResourceIDs  []uuid.UUID `validate:"required,min=1,max=20,dive"` // evidence photos, mandatory
	ReturnOption string      `validate:"required,min=1,max=100"`
}

// CreateBuyerRefund opens a refund. The buyer simultaneously commits to
// shipping the goods back: a return transport row is created on the spot and
// the refund starts in Shipping. Required: reason + photos + return option.
func (b *RefundHandler) CreateBuyerRefund(
	ctx restate.Context,
	params CreateBuyerRefundParams,
) (ordermodel.Refund, error) {
	var zero ordermodel.Refund
	var err error
	defer metrics.TrackHandler("order", "CreateBuyerRefund", &err)()

	if err = validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate create refund: %w", err)
	}
	if len(params.ResourceIDs) == 0 {
		return zero, ordermodel.ErrRefundEvidenceRequired
	}

	// Validate order ownership + paid state inside one durable Run so the
	// snapshot is journaled.
	type guardResult struct {
		Order  orderdb.OrderOrder `json:"order"`
		Item   orderdb.OrderItem  `json:"item"`
		Active bool               `json:"active"`
	}
	guard, err := restate.Run(ctx, func(rctx restate.RunContext) (guardResult, error) {
		order, e := b.Storage.Querier().GetOrder(rctx, orderdb.GetOrderParams{
			ID: uuid.NullUUID{UUID: params.OrderID, Valid: true},
		})
		if e != nil {
			return guardResult{}, fmt.Errorf("get order: %w", e)
		}
		if order.BuyerID != params.Account.ID {
			return guardResult{}, ordermodel.ErrItemNotOwnedByBuyer
		}

		items, e := b.Storage.Querier().ListItem(rctx, orderdb.ListItemParams{
			OrderID: []uuid.NullUUID{{UUID: params.OrderID, Valid: true}},
		})
		if e != nil {
			return guardResult{}, fmt.Errorf("list items: %w", e)
		}
		var anyItem orderdb.OrderItem
		for _, it := range items {
			if !it.DateCancelled.Valid {
				anyItem = it
				break
			}
		}
		if anyItem.ID == 0 {
			return guardResult{}, ordermodel.ErrItemAlreadyCancelled
		}

		// Order must have a settled positive tx in the buyer's session.
		txs, e := b.Storage.Querier().ListTransactionsBySession(rctx, anyItem.PaymentSessionID)
		if e != nil {
			return guardResult{}, fmt.Errorf("list txs: %w", e)
		}
		if _, paid := wfbase.FindOriginalCharge(txs); !paid {
			return guardResult{}, ordermodel.ErrRefundOrderNotPaid
		}

		active, e := b.Storage.Querier().HasActiveRefundForOrder(rctx, params.OrderID)
		if e != nil {
			return guardResult{}, fmt.Errorf("check active refund: %w", e)
		}
		return guardResult{Order: order, Item: anyItem, Active: active}, nil
	})
	if err != nil {
		return zero, fmt.Errorf("validate order for refund: %w", err)
	}
	if guard.Active {
		return zero, ordermodel.ErrRefundAlreadyAccepted
	}

	// Create return transport + refund row in the same Run so a failure mid-way
	// doesn't leave an orphan transport. Transport starts in NULL/Pending — the
	// workflow's mock timer (or the real provider webhook) will mark it Success
	// later, giving the buyer a window to withdraw while it is still Shipping.
	refund, err := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderRefund, error) {
		returnTransport, e := b.Storage.Querier().CreateDefaultTransport(rctx, orderdb.CreateDefaultTransportParams{
			Option: params.ReturnOption,
			Data:   json.RawMessage(`{"direction":"return","leg":"buyer-to-seller"}`),
		})
		if e != nil {
			return orderdb.OrderRefund{}, fmt.Errorf("create return transport: %w", e)
		}

		return b.Storage.Querier().CreateBuyerRefund(rctx, orderdb.CreateBuyerRefundParams{
			AccountID:         params.Account.ID,
			OrderID:           params.OrderID,
			Reason:            params.Reason,
			ReturnTransportID: returnTransport.ID,
		})
	})
	if err != nil {
		return zero, fmt.Errorf("create refund: %w", err)
	}

	// Attach the buyer's evidence photos to the refund via the common resource
	// system (RefType=Refund).
	resources, err := b.common.UpdateResources(ctx, commonbiz.UpdateResourcesParams{
		Account:     params.Account,
		RefType:     commondb.CommonResourceRefTypeRefund,
		RefID:       refund.ID,
		ResourceIDs: params.ResourceIDs,
	})
	if err != nil {
		return zero, fmt.Errorf("attach refund resources: %w", err)
	}

	// Wake the order's fulfillment workflow: its escrow loop snapshots the
	// refund state and drives this refund's lifecycle inline (mock
	// auto-deliver timer, seller review window, dispute escalation).
	if err = b.fulfillment.Send().OnRefundChanged(ctx, refund.OrderID); err != nil {
		return zero, fmt.Errorf("signal refund changed: %w", err)
	}
	if err = b.NotifyRefund(ctx, guard.Order.SellerID, accountmodel.NotiRefundRequested,
		"New refund request", "A buyer has shipped items back and requested a refund.", refund); err != nil {
		return zero, fmt.Errorf("notify refund: %w", err)
	}

	return ordermodel.Refund{OrderRefund: refund, Resources: resources}, nil
}
