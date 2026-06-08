package refund

import (
	"context"

	commonbiz "shopnexus-server/internal/module/common/biz"
	"shopnexus-server/internal/module/order/biz/workflow/fullfilment"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/shared/paginate"

	wfbase "shopnexus-server/internal/module/order/biz/workflow/base"
)

// RefundHandler implements RefundBiz over the workflow-shared core (it drives
// the refund credit flow) and signals the order's FulfillmentWorkflow directly.
type RefundHandler struct {
	*wfbase.Base

	common      commonbiz.CommonBizClient
	fulfillment fullfilment.FulfillmentWfClient
}

func New(c *wfbase.Base, common commonbiz.CommonBizClient, fulfillment fullfilment.FulfillmentWfClient) *RefundHandler {
	return &RefundHandler{c, common, fulfillment}
}

// RefundBiz covers the v2 refund lifecycle (buyer ships return → seller decides → admin if disputed).
type RefundBiz interface {
	ListBuyerRefunds(
		ctx context.Context,
		params ListBuyerRefundsParams,
	) (paginate.PaginateResult[ordermodel.Refund], error)
	ListSellerRefunds(
		ctx context.Context,
		params ListSellerRefundsParams,
	) (paginate.PaginateResult[ordermodel.Refund], error)
	CreateBuyerRefund(ctx context.Context, params CreateBuyerRefundParams) (ordermodel.Refund, error)
	WithdrawBuyerRefund(ctx context.Context, params WithdrawBuyerRefundParams) (ordermodel.Refund, error)
	SellerApproveRefund(ctx context.Context, params SellerActionParams) (ordermodel.Refund, error)
	SellerDisputeRefund(ctx context.Context, params SellerDisputeParams) (ordermodel.RefundDispute, error)
}
