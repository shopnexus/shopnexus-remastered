package refund

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"shopnexus-server/internal/infras/metrics"
	accountmodel "shopnexus-server/internal/module/account/model"
	commonbiz "shopnexus-server/internal/module/common/biz"
	commondb "shopnexus-server/internal/module/common/db/sqlc"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
	orderrepo "shopnexus-server/internal/module/order/repo"
	"shopnexus-server/internal/provider/transport"
	"shopnexus-server/internal/shared/pgsqlc"
	"shopnexus-server/internal/shared/validator"

	restate "github.com/restatedev/sdk-go"
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

	// TODO: lock here to prevent TOCTOU - maybe lock Refund?

	// decision: the order exists, belongs to the buyer, has a non-cancelled paid
	// item, and has no active refund.
	type decision struct {
		Order orderdb.OrderOrder
	}
	dec, err := restate.Run(ctx, func(rctx restate.RunContext) (decision, error) {
		var zero decision

		order, err := b.Storage.Querier().GetOrder(rctx, orderdb.GetOrderParams{
			ID: uuid.NullUUID{UUID: params.OrderID, Valid: true},
		})
		if err != nil {
			return zero, fmt.Errorf("get order: %w", err)
		}
		if order.BuyerID != params.Account.ID {
			return zero, ordermodel.ErrItemNotOwnedByBuyer
		}

		// Ensure the order has at least one non-cancelled item
		listItems, err := b.Storage.Querier().ListItem(rctx, orderdb.ListItemParams{
			OrderId: []uuid.UUID{params.OrderID},
		})
		if err != nil {
			return zero, fmt.Errorf("list items: %w", err)
		}
		var anyItem orderdb.OrderItem
		for _, it := range listItems.Data {
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
		txs, err := b.Storage.Querier().ListTransactionsBySession(rctx, anyItem.PaymentSessionID)
		if err != nil {
			return zero, fmt.Errorf("list txs: %w", err)
		}
		if _, paid := ordermodel.FindOriginalCharge(txs); !paid {
			return zero, ordermodel.ErrRefundOrderNotPaid
		}

		active, err := b.Storage.Querier().HasActiveRefundForOrder(rctx, params.OrderID)
		if err != nil {
			return zero, fmt.Errorf("check active refund: %w", err)
		}
		if active {
			return zero, ordermodel.ErrRefundAlreadyAccepted
		}

		return decision{Order: order}, nil
	})
	if err != nil {
		return zero, err
	}
	order := dec.Order

	transportClient, err := b.GetTransportClient(params.ReturnOption)
	if err != nil {
		return zero, fmt.Errorf("get transport client: %w", err)
	}

	// execution: book the return shipment, then create the return transport +
	// refund row in one tx. The shipment's tracking_id (in Data) is what the
	// delivery webhook matches to fire OnTransportDelivered → escrow refund phase.
	refund, err := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderRefund, error) {
		shipment, bErr := transportClient.Create(rctx, transport.CreateParams{
			Option:      params.ReturnOption,
			FromAddress: order.Address,
		})
		if bErr != nil {
			return orderdb.OrderRefund{}, fmt.Errorf("book return transport: %w", bErr)
		}
		transportData := map[string]any{"direction": "return", "leg": "buyer-to-seller"}
		_ = json.Unmarshal(shipment.Data, &transportData)
		trData, _ := json.Marshal(transportData)

		var refund orderdb.OrderRefund
		if err := b.Storage.Transact(rctx, func(s pgsqlc.Storage[*orderrepo.Repository]) error {
			returnTransport, err := s.Querier().CreateDefaultTransport(rctx, orderdb.CreateDefaultTransportParams{
				Option: params.ReturnOption,
				Data:   json.RawMessage(trData),
			})
			if err != nil {
				return fmt.Errorf("create return transport: %w", err)
			}

			refund, err = s.Querier().CreateBuyerRefund(rctx, orderdb.CreateBuyerRefundParams{
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
			return orderdb.OrderRefund{}, fmt.Errorf("create refund transaction: %w", err)
		}
		return refund, nil
	})
	if err != nil {
		return zero, err
	}

	// TODO(saga): tail steps below run outside the tx. Once de-journaled, a
	// retry hits the active-refund guard and skips them -> refund created but
	// resources/signal/notify lost. Needs a saga (compensate or re-entry resume).
	// Attach the buyer's evidence photos to the refund via the common resource
	// system (RefType=Refund).
	resources, err := b.common.Call().UpdateResources(ctx, commonbiz.UpdateResourcesParams{
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
