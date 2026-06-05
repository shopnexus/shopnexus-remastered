package orderbiz

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	accountbiz "shopnexus-server/internal/module/account/biz"
	accountmodel "shopnexus-server/internal/module/account/model"
	catalogbiz "shopnexus-server/internal/module/catalog/biz"
	catalogmodel "shopnexus-server/internal/module/catalog/model"
	commonbiz "shopnexus-server/internal/module/common/biz"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/provider/transport"
	"shopnexus-server/internal/provider/transport/ghtk"
	sharedmodel "shopnexus-server/internal/shared/model"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
	restate "github.com/restatedev/sdk-go"
	"github.com/samber/lo"
)

// QuoteTransportParams asks the buyer-facing transport quote endpoint for a
// per-item shipping cost preview before the checkout workflow is submitted.
type QuoteTransportParams struct {
	Account accountmodel.AuthenticatedAccount `validate:"required"`
	Address string                            `json:"address" validate:"required"`
	Items   []CheckoutItem                    `json:"items" validate:"required,min=1,dive"`
}

// QuoteTransportItemResult is the per-SKU quote: the cost is in the seller
// SPU's source currency (same convention as workflow_checkout step 5), so the
// FE must convert via the exchange-rate snapshot to the buyer's currency.
type QuoteTransportItemResult struct {
	SkuID    uuid.UUID `json:"sku_id"`
	Option   string    `json:"transport_option"`
	Cost     int64     `json:"cost"`
	Currency string    `json:"currency"`
}

type QuoteTransportResult struct {
	Items []QuoteTransportItemResult `json:"items"`
}

// QuoteTransport returns per-item shipping cost quotes without reserving
// inventory or creating any session. Mirrors the quote loop in
// CheckoutWorkflow.Run step 5 — kept in lockstep so the preview matches what
// the workflow will actually charge.
func (b *transportHandler) QuoteTransport(
	ctx restate.Context,
	params QuoteTransportParams,
) (QuoteTransportResult, error) {
	var zero QuoteTransportResult

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate quote transport: %w", err)
	}

	skuIDs := lo.Map(params.Items, func(i CheckoutItem, _ int) uuid.UUID { return i.SkuID })

	skus, err := b.catalog.ListProductSku(ctx, catalogbiz.ListProductSkuParams{
		ID: skuIDs,
	})
	if err != nil {
		return zero, fmt.Errorf("fetch product skus: %w", err)
	}
	if len(skus) != len(skuIDs) {
		return zero, ordermodel.ErrOrderItemNotFound
	}

	listSpu, err := b.catalog.ListProductSpu(ctx, catalogbiz.ListProductSpuParams{
		Account: params.Account,
		ID:      lo.Map(skus, func(s catalogmodel.ProductSku, _ int) uuid.UUID { return s.SpuID }),
	})
	if err != nil {
		return zero, fmt.Errorf("fetch product spus: %w", err)
	}

	skuMap := lo.KeyBy(skus, func(s catalogmodel.ProductSku) uuid.UUID { return s.ID })
	spuMap := lo.KeyBy(listSpu.Data, func(s catalogmodel.ProductSpu) uuid.UUID { return s.ID })

	sellerIDs := lo.Uniq(lo.Map(skus, func(s catalogmodel.ProductSku, _ int) uuid.UUID {
		return spuMap[s.SpuID].AccountID
	}))

	sellerContacts, err := b.account.GetDefaultContact(ctx, sellerIDs)
	if err != nil {
		return zero, fmt.Errorf("fetch seller contacts: %w", err)
	}

	results := make([]QuoteTransportItemResult, 0, len(params.Items))
	for _, item := range params.Items {
		sku, ok := skuMap[item.SkuID]
		if !ok {
			return zero, ordermodel.ErrOrderItemNotFound
		}
		spu, ok := spuMap[sku.SpuID]
		if !ok {
			return zero, ordermodel.ErrOrderItemNotFound
		}

		transportClient, tcErr := b.getTransportClient(item.TransportOption)
		if tcErr != nil {
			return zero, fmt.Errorf("get transport client: %w", tcErr)
		}

		sellerContact, ok := sellerContacts[spu.AccountID]
		if !ok {
			return zero, fmt.Errorf("seller contact not found: %w", ordermodel.ErrOrderItemNotFound)
		}

		quote, qErr := transportClient.Quote(ctx, transport.QuoteParams{
			Items: []transport.ItemMetadata{{
				SkuID:    item.SkuID,
				Quantity: item.Quantity,
			}},
			FromAddress: sellerContact.Address,
			ToAddress:   params.Address,
		})
		if qErr != nil {
			return zero, fmt.Errorf("quote transport for sku %s: %w", item.SkuID, qErr)
		}

		results = append(results, QuoteTransportItemResult{
			SkuID:    item.SkuID,
			Option:   item.TransportOption,
			Cost:     quote.Cost,
			Currency: spu.Currency,
		})
	}

	return QuoteTransportResult{Items: results}, nil
}

// See: https://docs.giaohangtietkiem.vn/webhook

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

// OnTransportResult updates a transport record's status and data field.
func (b *transportHandler) OnTransportResult(ctx restate.Context, params OnTransportResultParams) error {
	if err := validator.Validate(params); err != nil {
		return fmt.Errorf("validate on transport result: %w", err)
	}

	type transportInfo struct {
		TransportID int64  `json:"transport_id"`
		TrackingID  string `json:"tracking_id"`
	}

	// Step 1: Lookup by tracking ID, validate transition, update status.
	fetched, err := restate.Run(ctx, func(ctx restate.RunContext) (transportInfo, error) {
		var zero transportInfo

		tr, err := b.storage.Querier().GetTransportByTrackingID(ctx, json.RawMessage(`"`+params.TrackingID+`"`))
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

		if _, err = b.storage.Querier().UpdateTransportStatusByID(ctx, orderdb.UpdateTransportStatusByIDParams{
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
	})
	if err != nil {
		return fmt.Errorf("update transport status: %w", err)
	}

	// Step 2: If Delivered (Success), fetch orders on this transport and signal
	// PayoutWorkflow so it can re-arm / wake up its escrow-release evaluation.
	if orderdb.OrderStatus(params.Status) == orderdb.OrderStatusSuccess {
		order, err := restate.Run(ctx, func(ctx restate.RunContext) (orderdb.OrderOrder, error) {
			return b.storage.Querier().GetOrderByTransportID(ctx, fetched.TransportID)
		})
		if err != nil {
			return fmt.Errorf("fetch order by transport ID: %w", err)
		}
		// Notify buyer about delivery.
		meta, _ := json.Marshal(map[string]string{
			"tracking_id": fetched.TrackingID,
			"order_id":    order.ID.String(),
		})
		restate.ServiceSend(ctx, "Account", "CreateNotification").Send(accountbiz.CreateNotificationParams{
			AccountID: order.BuyerID,
			Type:      accountmodel.NotiTransportDelivered,
			Channel:   accountmodel.ChannelInApp,
			Title:     "Đơn hàng đã được giao",
			Content:   "Đơn hàng của bạn đã được giao thành công.",
			Metadata:  meta,
		})
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
	info, fetchErr := restate.Run(ctx, func(ctx restate.RunContext) (orderInfo, error) {
		r, err := b.storage.Querier().GetTransportWithOrder(ctx, fetched.TransportID)
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
		b.logger.Warn("skip notifications: could not fetch transport order info",
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
		restate.ServiceSend(ctx, "Account", "CreateNotification").Send(accountbiz.CreateNotificationParams{
			AccountID: info.BuyerID,
			Type:      accountmodel.NotiTransportFailed,
			Channel:   accountmodel.ChannelInApp,
			Title:     "Giao hàng thất bại",
			Content:   "Đơn hàng của bạn giao không thành công. Vui lòng liên hệ hỗ trợ.",
			Metadata:  meta,
		})
		restate.ServiceSend(ctx, "Account", "CreateNotification").Send(accountbiz.CreateNotificationParams{
			AccountID: info.SellerID,
			Type:      accountmodel.NotiSellerTransportFailed,
			Channel:   accountmodel.ChannelInApp,
			Title:     "Giao hàng thất bại",
			Content:   "Đơn hàng đã giao không thành công.",
			Metadata:  meta,
		})

	case orderdb.OrderStatusCancelled:
		restate.ServiceSend(ctx, "Account", "CreateNotification").Send(accountbiz.CreateNotificationParams{
			AccountID: info.BuyerID,
			Type:      accountmodel.NotiTransportCancelled,
			Channel:   accountmodel.ChannelInApp,
			Title:     "Đơn hàng đã bị hủy vận chuyển",
			Content:   "Đơn vận chuyển của bạn đã bị hủy.",
			Metadata:  meta,
		})
		restate.ServiceSend(ctx, "Account", "CreateNotification").Send(accountbiz.CreateNotificationParams{
			AccountID: info.SellerID,
			Type:      accountmodel.NotiSellerTransportCancelled,
			Channel:   accountmodel.ChannelInApp,
			Title:     "Đơn hàng đã bị hủy vận chuyển",
			Content:   "Đơn vận chuyển đã bị hủy.",
			Metadata:  meta,
		})
	}

	return nil
}

// SetupTransportMap registers the transport options in the central catalog.
// Clients themselves are built on demand — nothing is cached on the handler.
func (b *transportHandler) SetupTransportMap() error {
	configs := b.transportOptions()

	go func() {
		if err := b.common.UpsertOptions(context.Background(), commonbiz.UpsertOptionsParams{
			Type:    string(sharedmodel.OptionTypeTransport),
			Configs: configs,
		}); err != nil {
			b.logger.Warn("register transport options", slog.Any("error", err))
		}
	}()

	return nil
}

// transportFactory routes a transport Option to its provider-specific constructor.
func (b *transportHandler) transportFactory(cfg sharedmodel.Option) transport.Client {
	switch cfg.Provider {
	case "ghtk":
		return ghtk.NewClient(cfg)
	default:
		b.logger.Warn("unknown transport provider", "provider", cfg.Provider, "id", cfg.ID)
		return nil
	}
}

func (b *transportHandler) transportOptions() []sharedmodel.Option {
	var configs []sharedmodel.Option

	ghtkCfg := b.cfg.GHTK
	for _, method := range []string{ghtk.ServiceExpress, ghtk.ServiceStandard, ghtk.ServiceEconomy} {
		data, _ := json.Marshal(ghtk.Data{
			Method:   method,
			BaseURL:  ghtkCfg.BaseURL,
			APIKey:   ghtkCfg.APIKey,
			ClientID: ghtkCfg.ClientID,
			Secret:   ghtkCfg.Secret,
		})
		configs = append(configs, sharedmodel.Option{
			ID:          "ghtk_" + method,
			Type:        sharedmodel.OptionTypeTransport,
			Provider:    "ghtk",
			Name:        "Giao hàng tiết kiệm - " + method,
			Description: "Dịch vụ giao hàng nhanh của Giao hàng tiết kiệm",
			Data:        data,
		})
	}

	return configs
}

// getTransportClient builds a transport client on demand for the given option ID.
// The lookup walks the config-derived option list — no per-handler cache.
func (b *transportHandler) getTransportClient(option string) (transport.Client, error) {
	for _, cfg := range b.transportOptions() {
		if cfg.ID == option {
			if client := b.transportFactory(cfg); client != nil {
				return client, nil
			}
			break
		}
	}
	return nil, ordermodel.ErrUnknownTransportOption
}

type OnTransportResultParams struct {
	TrackingID string            `validate:"omitempty"`
	Status     ordermodel.Status `validate:"required,validateFn=Valid"`
	Data       json.RawMessage   `validate:"omitempty"`
}
