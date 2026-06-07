package refund

import (
	"context"

	"shopnexus-server/internal/module/order/biz/workflow/fullfilment"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/shared/paginate"

	wfbase "shopnexus-server/internal/module/order/biz/workflow/base"
)

// RefundHandler implements RefundBiz over the workflow-shared core (it drives
// the refund credit flow) and signals the order's FulfillmentWorkflow directly.
type RefundHandler struct {
	*wfbase.Base

	fulfillment fullfilment.FulfillmentWfClient
}

func New(c *wfbase.Base, fulfillment fullfilment.FulfillmentWfClient) *RefundHandler {
	return &RefundHandler{c, fulfillment}
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

// DisputeAttachment is a single piece of evidence (image URL + meta). Used by
// the refund-create and seller-dispute flows.
type DisputeAttachment struct {
	URL  string `json:"url"  validate:"required,url,max=1000"`
	Kind string `json:"kind" validate:"omitempty,max=50"`
	Name string `json:"name" validate:"omitempty,max=255"`
}
