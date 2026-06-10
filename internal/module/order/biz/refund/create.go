package refund

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"shopnexus-server/internal/infras/metrics"
	accountmodel "shopnexus-server/internal/module/account/model"
	commonbiz "shopnexus-server/internal/module/common/biz"
	commondb "shopnexus-server/internal/module/common/db/sqlc"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/shared/pgsqlc"
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
	ctx context.Context,
	params CreateBuyerRefundParams,
) (ordermodel.Refund, error) {
	var zero ordermodel.Refund
	var err error
	defer metrics.TrackHandler("order", "CreateBuyerRefund", &err)()

	if err = validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate create refund: %w", err)
	}

	// TODO: lock here to prevent TOCTOU - maybe lock Refund?

	// Check if the order exists & belongs to the buyer
	order, err := b.Storage.Querier().GetOrder(ctx, orderdb.GetOrderParams{
		ID: uuid.NullUUID{UUID: params.OrderID, Valid: true},
	})
	if err != nil {
		return zero, fmt.Errorf("get order: %w", err)
	}
	if order.BuyerID != params.Account.ID {
		return zero, ordermodel.ErrItemNotOwnedByBuyer
	}

	// Ensure the order has at least one non-cancelled item
	listItems, err := b.Storage.Querier().ListItem(ctx, orderdb.ListItemParams{
		OrderId: []uuid.UUID{params.OrderID},
	})
	if err != nil {
		return zero, fmt.Errorf("list items: %w", err)
	}
	items := listItems.Data
	var anyItem orderdb.OrderItem
	for _, it := range items {
		if !it.DateCancelled.Valid {
			anyItem = it
			break
		}
	}
	// If all items are cancelled, the order is effectively cancelled and thus ineligible for refunds.
	if anyItem.ID == 0 {
		return zero, ordermodel.ErrItemAlreadyCancelled
	}

	// Order must have a settled positive tx in the buyer's session.
	txs, err := b.Storage.Querier().ListTransactionsBySession(ctx, anyItem.PaymentSessionID)
	if err != nil {
		return zero, fmt.Errorf("list txs: %w", err)
	}
	if _, paid := ordermodel.FindOriginalCharge(txs); !paid {
		return zero, ordermodel.ErrRefundOrderNotPaid
	}

	active, err := b.Storage.Querier().HasActiveRefundForOrder(ctx, params.OrderID)
	if err != nil {
		return zero, fmt.Errorf("check active refund: %w", err)
	}
	if active {
		return zero, ordermodel.ErrRefundAlreadyAccepted
	}

	var refund orderdb.OrderRefund

	if err := b.Storage.Transact(ctx, func(s pgsqlc.Storage[*orderdb.Queries]) error {
		returnTransport, err := s.Querier().CreateDefaultTransport(ctx, orderdb.CreateDefaultTransportParams{
			Option: params.ReturnOption,
			Data:   json.RawMessage(`{"direction":"return","leg":"buyer-to-seller"}`),
		})
		if err != nil {
			return fmt.Errorf("create return transport: %w", err)
		}

		refund, err = s.Querier().CreateBuyerRefund(ctx, orderdb.CreateBuyerRefundParams{
			AccountID:         params.Account.ID,
			OrderID:           params.OrderID,
			Reason:            params.Reason,
			ReturnTransportID: returnTransport.ID,
		})
		if err != nil {
			return fmt.Errorf("create refund: %w", err)
		}

		return nil
	}); err != nil {
		return zero, fmt.Errorf("create refund transaction: %w", err)
	}

	// TODO(saga): tail steps below run outside the tx. Once de-journaled, a
	// retry hits the active-refund guard and skips them -> refund created but
	// resources/signal/notify lost. Needs a saga (compensate or re-entry resume).
	// Attach the buyer's evidence photos to the refund via the common resource
	// system (RefType=Refund).
	resources, err := b.common.Guaranteed().UpdateResources(ctx, commonbiz.UpdateResourcesParams{
		Account:     params.Account,
		RefType:     commondb.CommonResourceRefTypeRefund,
		RefID:       refund.ID,
		ResourceIDs: params.ResourceIDs,
	})
	if err != nil {
		return zero, fmt.Errorf("attach refund resources: %w", err)
	}

	// Wake the order's fulfillment workflow
	if err = b.fulfillment.Send().OnRefundChanged(ctx, refund.OrderID); err != nil {
		return zero, fmt.Errorf("signal refund changed: %w", err)
	}
	if err = b.NotifyRefund(ctx, order.SellerID, accountmodel.NotiRefundRequested,
		"New refund request", "A buyer has shipped items back and requested a refund.", refund); err != nil {
		return zero, fmt.Errorf("notify refund: %w", err)
	}

	return ordermodel.Refund{OrderRefund: refund, Resources: resources}, nil
}
