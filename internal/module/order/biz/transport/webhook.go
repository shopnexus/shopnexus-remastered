package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	accountbiz "shopnexus-server/internal/module/account/biz"
	accountmodel "shopnexus-server/internal/module/account/model"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
)

// validTransitions defines which OrderStatus transitions are allowed on the transport table.
var validTransitions = map[orderdb.OrderStatus]map[orderdb.OrderStatus]bool{
	orderdb.OrderStatusPending: {
		orderdb.OrderStatusProcessing: true, // LabelCreated / InTransit / OutForDelivery
		orderdb.OrderStatusFailed:     true,
		orderdb.OrderStatusCancelled:  true,
	},
	orderdb.OrderStatusProcessing: {
		orderdb.OrderStatusSuccess:   true, // Delivered
		orderdb.OrderStatusFailed:    true,
		orderdb.OrderStatusCancelled: true,
	},
	// Terminal states: Success (Delivered), Failed, Cancelled
	orderdb.OrderStatusSuccess:   {},
	orderdb.OrderStatusFailed:    {orderdb.OrderStatusProcessing: true}, // redelivery
	orderdb.OrderStatusCancelled: {},
}

type OnTransportResultParams struct {
	TrackingID string              `validate:"omitempty"`
	Status     orderdb.OrderStatus `validate:"required,validateFn=Valid"`
	Data       json.RawMessage     `validate:"omitempty"`
}

// OnTransportResult updates a transport record's status and data field.
func (b *TransportHandler) OnTransportResult(ctx context.Context, params OnTransportResultParams) error {
	if err := validator.Validate(params); err != nil {
		return fmt.Errorf("validate on transport result: %w", err)
	}

	type transportInfo struct {
		TransportID int64  `json:"transport_id"`
		TrackingID  string `json:"tracking_id"`
	}

	// Step 1: Lookup by tracking ID, validate transition, update status.
	fetched, err := func() (transportInfo, error) {
		var zero transportInfo

		tr, err := b.Storage.Querier().GetTransportByTrackingID(ctx, json.RawMessage(`"`+params.TrackingID+`"`))
		if err != nil {
			return zero, ordermodel.ErrOrderNotFound
		}

		currentStatus := orderdb.OrderStatusPending
		if tr.Status.Valid {
			currentStatus = tr.Status.OrderStatus
		}

		allowed, ok := validTransitions[currentStatus]
		if !ok || !allowed[orderdb.OrderStatus(params.Status)] {
			return zero, ordermodel.ErrTransportStatusInvalid.Fmt(currentStatus, params.Status)
		}

		dataJSON := params.Data
		if len(dataJSON) == 0 {
			dataJSON = json.RawMessage("{}")
		}

		if _, err = b.Storage.Querier().UpdateTransportStatusByID(ctx, orderdb.UpdateTransportStatusByIDParams{
			ID:     tr.ID,
			Status: orderdb.NullOrderStatus{OrderStatus: orderdb.OrderStatus(params.Status), Valid: true},
			Data:   dataJSON,
		}); err != nil {
			return zero, fmt.Errorf("db update transport status: %w", err)
		}

		return transportInfo{
			TransportID: tr.ID,
			TrackingID:  params.TrackingID,
		}, nil
	}()
	if err != nil {
		return fmt.Errorf("update transport status: %w", err)
	}

	// Step 2: If Delivered (Success), fetch orders on this transport and signal
	// the order's FulfillmentWorkflow so it can re-arm its escrow-release evaluation.
	if orderdb.OrderStatus(params.Status) == orderdb.OrderStatusSuccess {
		order, err := b.Storage.Querier().GetOrderByTransportID(ctx, fetched.TransportID)
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
	info, fetchErr := func() (orderInfo, error) {
		r, err := b.Storage.Querier().GetTransportWithOrder(ctx, fetched.TransportID)
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
	}()
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

	switch orderdb.OrderStatus(params.Status) {
	case orderdb.OrderStatusFailed:
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

	case orderdb.OrderStatusCancelled:
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
	}

	return nil
}
