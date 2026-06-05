package orderbiz

import (
	"context"

	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/provider/payment"
	sharedmodel "shopnexus-server/internal/shared/model"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/pgsqlc"

	"github.com/google/uuid"
)

//go:generate go run shopnexus-server/cmd/genrestate -interface OrderBiz -service Order
type OrderBiz interface {
	BuyerOrderBiz
	SellerOrderBiz
	CartBiz
	PaymentBiz
	RefundBiz
	DisputeBiz
	TransportBiz
	ReviewBiz
	DashboardBiz
	OrderUtilBiz
}

// BuyerOrderBiz covers the buyer's view of pending items and confirmed orders.
type BuyerOrderBiz interface {
	ListBuyerPendingItems(
		ctx context.Context,
		params ListBuyerPendingItemsParams,
	) (paginate.PaginateResult[ordermodel.OrderItem], error)
	CancelBuyerPending(ctx context.Context, params CancelBuyerPendingParams) error
	RefundPendingItem(ctx context.Context, params RefundPendingItemParams) error
	ListBuyerPendingOrders(ctx context.Context, params ListBuyerPendingOrdersParams) (paginate.PaginateResult[ordermodel.Order], error)
	ListBuyerCompletedOrders(ctx context.Context, params ListBuyerCompletedOrdersParams) (paginate.PaginateResult[ordermodel.Order], error)
	ListBuyerCancelledOrders(ctx context.Context, params ListBuyerCancelledOrdersParams) (paginate.PaginateResult[ordermodel.Order], error)
	ListBuyerCancelledItems(ctx context.Context, params ListBuyerCancelledItemsParams) (paginate.PaginateResult[ordermodel.OrderItem], error)
	GetBuyerOrder(ctx context.Context, orderID uuid.UUID) (ordermodel.Order, error)
	GetCheckoutSummary(ctx context.Context, params GetCheckoutSummaryParams) (ordermodel.CheckoutSummary, error)
}

// SellerOrderBiz covers the seller's incoming pending items and confirmed orders.
type SellerOrderBiz interface {
	ListSellerPendingItems(
		ctx context.Context,
		params ListSellerPendingItemsParams,
	) (paginate.PaginateResult[ordermodel.OrderItem], error)
	RejectSellerPending(ctx context.Context, params RejectSellerPendingParams) error
	GetSellerOrder(ctx context.Context, orderID uuid.UUID) (ordermodel.Order, error)
	ListSellerConfirmed(
		ctx context.Context,
		params ListSellerConfirmedParams,
	) (paginate.PaginateResult[ordermodel.Order], error)
}

// CartBiz covers the buyer's shopping cart.
type CartBiz interface {
	GetCart(ctx context.Context, params GetCartParams) ([]ordermodel.CartItem, error)
	UpdateCart(ctx context.Context, params UpdateCartParams) error
	ClearCart(ctx context.Context, params ClearCartParams) error
}

// PaymentBiz covers payment-result intake and gateway-URL reuse.
type PaymentBiz interface {
	// OnPaymentResult is the payment webhook entrypoint — gateway providers and
	// internal callers route through here. MarkTxSuccess/MarkTxFailed are
	// package-internal helpers.
	OnPaymentResult(ctx context.Context, params payment.Notification) error

	// GetReusableGatewayURL tells the caller whether the session has a reusable
	// Pending+not-expired gateway URL, or whether the workflow needs to be
	// signaled to spawn a fresh attempt. The echo handler combines this with a
	// workflow.RequestNewPaymentURL call when no reusable URL exists.
	GetReusableGatewayURL(ctx context.Context, sessionID uuid.UUID) (ReusableGatewayURLState, error)
}

// RefundBiz covers the v2 refund lifecycle (buyer ships return → seller decides
// → admin if disputed).
type RefundBiz interface {
	ListBuyerRefunds(ctx context.Context, params ListBuyerRefundsParams) (paginate.PaginateResult[ordermodel.Refund], error)
	ListSellerRefunds(ctx context.Context, params ListSellerRefundsParams) (paginate.PaginateResult[ordermodel.Refund], error)
	CreateBuyerRefund(ctx context.Context, params CreateBuyerRefundParams) (ordermodel.Refund, error)
	WithdrawBuyerRefund(ctx context.Context, params WithdrawBuyerRefundParams) (ordermodel.Refund, error)
	SellerApproveRefund(ctx context.Context, params SellerActionParams) (ordermodel.Refund, error)
	SellerDisputeRefund(ctx context.Context, params SellerDisputeParams) (ordermodel.RefundDispute, error)
	MarkRefundDelivered(ctx context.Context, params MarkRefundDeliveredParams) (ordermodel.Refund, error)
	AutoAcceptRefund(ctx context.Context, params AutoAcceptRefundParams) (ordermodel.Refund, error)
}

// DisputeBiz covers refund disputes (seller-initiated, admin-resolved).
type DisputeBiz interface {
	ListRefundDisputes(ctx context.Context, params ListRefundDisputesParams) (paginate.PaginateResult[ordermodel.RefundDispute], error)
	GetRefundDispute(ctx context.Context, params GetRefundDisputeParams) (ordermodel.RefundDispute, error)
	AdminUpholdDispute(ctx context.Context, params AdminDisputeDecisionParams) (ordermodel.RefundDispute, error)
	AdminDismissDispute(ctx context.Context, params AdminDisputeDecisionParams) (ordermodel.RefundDispute, error)
}

// TransportBiz covers transport webhooks and shipping-cost quoting.
type TransportBiz interface {
	OnTransportResult(ctx context.Context, params OnTransportResultParams) error
	// QuoteTransport returns per-item shipping cost previews for the buyer's
	// checkout summary. Side-effect free — no inventory, no session.
	QuoteTransport(ctx context.Context, params QuoteTransportParams) (QuoteTransportResult, error)
}

// ReviewBiz covers product-review eligibility checks.
type ReviewBiz interface {
	HasPurchasedProduct(ctx context.Context, params HasPurchasedProductParams) (bool, error)
	ListReviewableOrders(ctx context.Context, params ListReviewableOrdersParams) ([]ReviewableOrder, error)
	ValidateOrderForReview(ctx context.Context, params ValidateOrderForReviewParams) (bool, error)
}

// DashboardBiz covers seller dashboard aggregates.
type DashboardBiz interface {
	GetSellerOrderStats(ctx context.Context, params GetSellerOrderStatsParams) (SellerOrderStats, error)
	GetSellerOrderTimeSeries(
		ctx context.Context,
		params GetSellerOrderTimeSeriesParams,
	) ([]SellerOrderTimeSeriesPoint, error)
	GetSellerPendingActions(ctx context.Context, params GetSellerPendingActionsParams) (SellerPendingActions, error)
	GetSellerTopProducts(ctx context.Context, params GetSellerTopProductsParams) ([]SellerTopProduct, error)
}

// OrderUtilBiz covers cross-cutting utilities (currency inference, provider options).
type OrderUtilBiz interface {
	InferCurrency(ctx context.Context, accountID uuid.UUID) (string, error)
	GetOptions(ctx context.Context, params GetOptionsParams) ([]sharedmodel.Option, error)
}
type OrderStorage = pgsqlc.Storage[*orderdb.Queries]
