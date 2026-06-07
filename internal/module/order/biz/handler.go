package orderbiz

import (
	"context"

	"shopnexus-server/internal/module/order/biz/base"
	buyerorder "shopnexus-server/internal/module/order/biz/buyer_order"
	"shopnexus-server/internal/module/order/biz/cart"
	"shopnexus-server/internal/module/order/biz/dashboard"
	"shopnexus-server/internal/module/order/biz/dispute"
	orderpayment "shopnexus-server/internal/module/order/biz/payment"
	"shopnexus-server/internal/module/order/biz/refund"
	"shopnexus-server/internal/module/order/biz/review"
	sellerorder "shopnexus-server/internal/module/order/biz/seller_order"
	ordertransport "shopnexus-server/internal/module/order/biz/transport"
	sharedmodel "shopnexus-server/internal/shared/model"
)

// OrderHandler is the aggregate Restate service handler. Embedding every domain
// sub-handler promotes their methods up to satisfy OrderBiz; embedding
// *base.Base directly keeps the shared helpers unambiguous (depth 1 beats
// the depth-2 copies carried inside each sub-handler).
type OrderHandler struct {
	*base.Base
	*buyerorder.BuyerHandler
	*sellerorder.SellerHandler
	*cart.CartHandler
	*orderpayment.PaymentHandler
	*refund.RefundHandler
	*dispute.DisputeHandler
	*ordertransport.TransportHandler
	*review.ReviewHandler
	*dashboard.DashboardHandler
}

// NewOrderHandler assembles the DI-provided domain sub-handlers into the
// aggregate Restate service.
func NewOrderHandler(
	b *base.Base,
	buyerHandler *buyerorder.BuyerHandler,
	sellerHandler *sellerorder.SellerHandler,
	cartHandler *cart.CartHandler,
	paymentHandler *orderpayment.PaymentHandler,
	refundHandler *refund.RefundHandler,
	disputeHandler *dispute.DisputeHandler,
	transportHandler *ordertransport.TransportHandler,
	reviewHandler *review.ReviewHandler,
	dashboardHandler *dashboard.DashboardHandler,
) *OrderHandler {
	return &OrderHandler{
		Base:             b,
		BuyerHandler:     buyerHandler,
		SellerHandler:    sellerHandler,
		CartHandler:      cartHandler,
		PaymentHandler:   paymentHandler,
		RefundHandler:    refundHandler,
		DisputeHandler:   disputeHandler,
		TransportHandler: transportHandler,
		ReviewHandler:    reviewHandler,
		DashboardHandler: dashboardHandler,
	}
}

type GetOptionsParams struct {
	Type sharedmodel.OptionType // empty = all
}

func (b *OrderHandler) ServiceName() string {
	return "Order"
}

// GetOptions returns serializable Option configs (payment + transport providers).
func (b *OrderHandler) GetOptions(ctx context.Context, params GetOptionsParams) ([]sharedmodel.Option, error) {
	out := make([]sharedmodel.Option, 0)
	if params.Type == "" || params.Type == sharedmodel.OptionTypePayment {
		out = append(out, b.PaymentConfigs()...)
	}
	if params.Type == "" || params.Type == sharedmodel.OptionTypeTransport {
		out = append(out, b.TransportOptions()...)
	}
	return out, nil
}
