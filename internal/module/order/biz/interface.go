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
	"shopnexus-server/internal/module/order/biz/workflow/checkout"
	"shopnexus-server/internal/module/order/biz/workflow/fullfilment"
	ordermodel "shopnexus-server/internal/module/order/model"
	sharedmodel "shopnexus-server/internal/shared/model"

	"github.com/google/uuid"
)

//go:generate go run shopnexus-server/cmd/genrestate -interface OrderBiz -service Order
type OrderBiz interface {
	buyerorder.BuyerOrderBiz
	sellerorder.SellerOrderBiz
	cart.CartBiz
	orderpayment.PaymentBiz
	refund.RefundBiz
	dispute.DisputeBiz
	ordertransport.TransportBiz
	review.ReviewBiz
	dashboard.DashboardBiz

	// Core functions
	InferCurrency(ctx context.Context, accountID uuid.UUID) (string, error)
	GetOptions(ctx context.Context, params GetOptionsParams) ([]sharedmodel.Option, error)
}

type OrderStorage = base.OrderStorage

// Re-exported aliases: orderbiz stays the single import surface for transports
// and other modules; the per-domain packages remain an internal layout.

type (
	CheckoutItem            = base.CheckoutItem
	CreditFromSessionParams = ordermodel.CreditFromSessionParams

	ListBuyerPendingItemsParams    = buyerorder.ListBuyerPendingItemsParams
	CancelBuyerPendingParams       = buyerorder.CancelBuyerPendingParams
	RefundPendingItemParams        = buyerorder.RefundPendingItemParams
	ListBuyerPendingOrdersParams   = buyerorder.ListBuyerPendingOrdersParams
	ListBuyerCompletedOrdersParams = buyerorder.ListBuyerCompletedOrdersParams
	ListBuyerCancelledOrdersParams = buyerorder.ListBuyerCancelledOrdersParams
	ListBuyerCancelledItemsParams  = buyerorder.ListBuyerCancelledItemsParams
	GetCheckoutSummaryParams       = buyerorder.GetCheckoutSummaryParams

	ListSellerPendingItemsParams = sellerorder.ListSellerPendingItemsParams
	ConfirmSellerPendingParams   = sellerorder.ConfirmSellerPendingParams
	ConfirmSellerPendingResult   = sellerorder.ConfirmSellerPendingResult
	RejectSellerPendingParams    = sellerorder.RejectSellerPendingParams
	ListSellerConfirmedParams    = sellerorder.ListSellerConfirmedParams

	GetCartParams    = cart.GetCartParams
	UpdateCartParams = cart.UpdateCartParams
	ClearCartParams  = cart.ClearCartParams

	CreateBuyerRefundParams   = refund.CreateBuyerRefundParams
	WithdrawBuyerRefundParams = refund.WithdrawBuyerRefundParams
	SellerActionParams        = refund.SellerActionParams
	SellerDisputeParams       = refund.SellerDisputeParams
	ListBuyerRefundsParams    = refund.ListBuyerRefundsParams
	ListSellerRefundsParams   = refund.ListSellerRefundsParams

	ListRefundDisputesParams   = dispute.ListRefundDisputesParams
	GetRefundDisputeParams     = dispute.GetRefundDisputeParams
	AdminDisputeDecisionParams = dispute.AdminDisputeDecisionParams

	OnTransportResultParams  = ordertransport.OnTransportResultParams
	QuoteTransportParams     = ordertransport.QuoteTransportParams
	QuoteTransportResult     = ordertransport.QuoteTransportResult
	QuoteTransportItemResult = ordertransport.QuoteTransportItemResult

	HasPurchasedProductParams    = review.HasPurchasedProductParams
	ListReviewableOrdersParams   = review.ListReviewableOrdersParams
	ReviewableOrder              = review.ReviewableOrder
	ValidateOrderForReviewParams = review.ValidateOrderForReviewParams

	GetSellerOrderStatsParams      = dashboard.GetSellerOrderStatsParams
	SellerOrderStats               = dashboard.SellerOrderStats
	GetSellerOrderTimeSeriesParams = dashboard.GetSellerOrderTimeSeriesParams
	SellerOrderTimeSeriesPoint     = dashboard.SellerOrderTimeSeriesPoint
	GetSellerPendingActionsParams  = dashboard.GetSellerPendingActionsParams
	SellerPendingActions           = dashboard.SellerPendingActions
	GetSellerTopProductsParams     = dashboard.GetSellerTopProductsParams
	SellerTopProduct               = dashboard.SellerTopProduct
	GetSellerDashboardParams       = dashboard.GetSellerDashboardParams
	SellerDashboard                = dashboard.SellerDashboard

	CreateProductReviewParams       = review.CreateProductReviewParams
	ListReviewableOrdersBySpuParams = review.ListReviewableOrdersBySpuParams

	CheckoutWorkflow       = checkout.CheckoutWorkflow
	CheckoutWorkflowInput  = checkout.CheckoutWorkflowInput
	CheckoutWorkflowOutput = checkout.CheckoutWorkflowOutput
	CheckoutWfClient       = checkout.CheckoutWfClient
	FulfillmentWorkflow    = fullfilment.FulfillmentWorkflow
	FulfillmentInput       = fullfilment.FulfillmentInput
	FulfillmentOutput      = fullfilment.FulfillmentOutput
	FulfillmentWfClient    = fullfilment.FulfillmentWfClient
)

// WorkflowForSession maps payment_session.kind to (workflowName, workflowID).
func WorkflowForSession(s ordermodel.PaymentSession) (string, string) {
	return orderpayment.WorkflowForSession(s)
}
