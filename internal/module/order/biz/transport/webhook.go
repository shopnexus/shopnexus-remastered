package transport

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	accountbiz "shopnexus-server/internal/module/account/biz"
	accountmodel "shopnexus-server/internal/module/account/model"
	ordermodel "shopnexus-server/internal/module/order/model"
	orderrepo "shopnexus-server/internal/module/order/repo"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/jackc/pgx/v5"
	restate "github.com/restatedev/sdk-go"
)

// validTransitions defines which Status transitions are allowed on the transport table.
var validTransitions = map[ordermodel.Status]map[ordermodel.Status]bool{
	ordermodel.StatusPending: {
		ordermodel.StatusProcessing: true, // LabelCreated / InTransit / OutForDelivery
		ordermodel.StatusFailed:     true,
		ordermodel.StatusCancelled:  true,
	},
	ordermodel.StatusProcessing: {
		ordermodel.StatusSuccess:   true, // Delivered
		ordermodel.StatusFailed:    true,
		ordermodel.StatusCancelled: true,
	},
	// Terminal states: Success (Delivered), Failed, Cancelled
	ordermodel.StatusSuccess:   {},
	ordermodel.StatusFailed:    {},
	ordermodel.StatusCancelled: {},
}

type OnTransportResultParams struct {
	TrackingID string            `validate:"omitempty"`
	Status     ordermodel.Status `validate:"required,validateFn=Valid"`
	Data       json.RawMessage   `validate:"omitempty"`
}

// OnTransportResult updates a transport record's status and data field.
func (b *TransportHandler) OnTransportResult(ctx restate.Context, params OnTransportResultParams) error {
	if err := validator.Validate(params); err != nil {
		return fmt.Errorf("validate on transport result: %w", err)
	}

	type transportInfo struct {
		TransportID int64  `json:"transport_id"`
		TrackingID  string `json:"tracking_id"`
	}

	// execution: lookup by tracking ID, validate the transition, update status.
	fetched, err := restate.Run(ctx, func(rctx restate.RunContext) (transportInfo, error) {
		var zero transportInfo

		tr, err := b.Storage.Querier().GetTransportByTrackingID(rctx, params.TrackingID)
		if err != nil {
			return zero, ordermodel.ErrOrderNotFound
		}

		currentStatus := ordermodel.StatusPending
		if tr.Status.Valid {
			currentStatus = ordermodel.Status(tr.Status.String)
		}

		allowed, ok := validTransitions[currentStatus]
		if !ok || !allowed[params.Status] {
			return zero, ordermodel.ErrTransportStatusInvalid.Fmt(currentStatus, params.Status)
		}

		dataJSON := params.Data
		if len(dataJSON) == 0 {
			dataJSON = json.RawMessage("{}")
		}

		if _, err = b.Storage.Querier().UpdateTransportStatusByID(rctx, orderrepo.UpdateTransportStatusByIDParams{
			ID:     tr.ID,
			Status: null.StringFrom(string(params.Status)),
			Data:   dataJSON,
		}); err != nil {
			return zero, fmt.Errorf("db update transport status: %w", err)
		}

		return transportInfo{
			TransportID: tr.ID,
			TrackingID:  params.TrackingID,
		}, nil
	})
	if err != nil {
		return fmt.Errorf("update transport status: %w", err)
	}

	// tail: If Delivered (Success), fetch the order on this transport and notify
	// the buyer. The notify Send self-journals.
	if params.Status == ordermodel.StatusSuccess {
		// First check whether this transport is the RETURN leg of a refund
		// (keyed by return_transport_id). If so, signal the fulfillment
		// workflow that the return was delivered and stop — no buyer notify.
		refund, rErr := restate.Run(ctx, func(rctx restate.RunContext) (ordermodel.Refund, error) {
			return b.Storage.Querier().GetRefundByReturnTransportID(rctx, fetched.TransportID)
		})
		switch {
		case rErr == nil:
			if err = b.fulfillment.Send().OnTransportDelivered(
				ctx,
				refund.OrderID,
				ordermodel.TransportDeliveredSignal{RefundID: refund.ID},
			); err != nil {
				return fmt.Errorf("signal return delivered: %w", err)
			}
			return nil
		case errors.Is(rErr, pgx.ErrNoRows):
			// Not a return leg — fall through to the forward-leg path below.
		default:
			return fmt.Errorf("lookup refund by return transport: %w", rErr)
		}

		order, err := restate.Run(ctx, func(rctx restate.RunContext) (ordermodel.Order, error) {
			return b.Storage.Querier().GetOrderByTransportID(rctx, fetched.TransportID)
		})
		if err != nil {
			return fmt.Errorf("fetch order by transport ID: %w", err)
		}
		// Notify buyer about delivery.
		meta, _ := json.Marshal(map[string]string{
			"tracking_id": fetched.TrackingID,
			"order_id":    order.ID.String(),
		})
		if err = b.Notify(ctx, accountbiz.CreateNotificationParams{
			AccountID: order.BuyerID,
			Type:      accountmodel.NotiTransportDelivered,
			Channel:   accountmodel.ChannelInApp,
			Title:     "Đơn hàng đã được giao",
			Content:   "Đơn hàng của bạn đã được giao thành công.",
			Metadata:  meta,
		}); err != nil {
			return fmt.Errorf("notify buyer: %w", err)
		}
		return nil
	}

	// Step 3: Fire notifications for Failed / Cancelled statuses.
	// We need buyer + seller IDs from the order joined to this transport.
	type orderInfo struct {
		BuyerID  uuid.UUID `json:"buyer_id"`
		SellerID uuid.UUID `json:"seller_id"`
		OrderID  uuid.UUID `json:"order_id"`
		HasOrder bool      `json:"has_order"`
	}
	info, fetchErr := restate.Run(ctx, func(rctx restate.RunContext) (orderInfo, error) {
		r, err := b.Storage.Querier().GetTransportWithOrder(rctx, fetched.TransportID)
		if err != nil {
			// Transport may not yet be linked to an order (early status updates).
			return orderInfo{HasOrder: false}, nil
		}
		return orderInfo{
			BuyerID:  r.OrderBuyerID,
			SellerID: r.OrderSellerID,
			OrderID:  r.OrderID,
			HasOrder: true,
		}, nil
	})
	if fetchErr != nil {
		b.Logger.Warn("skip notifications: could not fetch transport order info",
			slog.String("tracking_id", params.TrackingID),
			slog.Any("error", fetchErr))
		return nil
	}
	if !info.HasOrder {
		return nil
	}

	meta, _ := json.Marshal(map[string]string{
		"tracking_id": params.TrackingID,
		"order_id":    info.OrderID.String(),
	})

	switch params.Status {
	case ordermodel.StatusFailed:
		if err = b.Notify(ctx, accountbiz.CreateNotificationParams{
			AccountID: info.BuyerID,
			Type:      accountmodel.NotiTransportFailed,
			Channel:   accountmodel.ChannelInApp,
			Title:     "Giao hàng thất bại",
			Content:   "Đơn hàng của bạn giao không thành công. Vui lòng liên hệ hỗ trợ.",
			Metadata:  meta,
		}); err != nil {
			return fmt.Errorf("notify buyer: %w", err)
		}
		if err = b.Notify(ctx, accountbiz.CreateNotificationParams{
			AccountID: info.SellerID,
			Type:      accountmodel.NotiSellerTransportFailed,
			Channel:   accountmodel.ChannelInApp,
			Title:     "Giao hàng thất bại",
			Content:   "Đơn hàng đã giao không thành công.",
			Metadata:  meta,
		}); err != nil {
			return fmt.Errorf("notify seller: %w", err)
		}

	case ordermodel.StatusCancelled:
		if err = b.Notify(ctx, accountbiz.CreateNotificationParams{
			AccountID: info.BuyerID,
			Type:      accountmodel.NotiTransportCancelled,
			Channel:   accountmodel.ChannelInApp,
			Title:     "Đơn hàng đã bị hủy vận chuyển",
			Content:   "Đơn vận chuyển của bạn đã bị hủy.",
			Metadata:  meta,
		}); err != nil {
			return fmt.Errorf("notify buyer: %w", err)
		}
		if err = b.Notify(ctx, accountbiz.CreateNotificationParams{
			AccountID: info.SellerID,
			Type:      accountmodel.NotiSellerTransportCancelled,
			Channel:   accountmodel.ChannelInApp,
			Title:     "Đơn hàng đã bị hủy vận chuyển",
			Content:   "Đơn vận chuyển đã bị hủy.",
			Metadata:  meta,
		}); err != nil {
			return fmt.Errorf("notify seller: %w", err)
		}
	case ordermodel.StatusPending, ordermodel.StatusProcessing, ordermodel.StatusSuccess:
	}

	return nil
}
