package dispute

import (
	"context"

	commonbiz "shopnexus-server/internal/module/common/biz"
	orderbase "shopnexus-server/internal/module/order/biz/base"
	"shopnexus-server/internal/module/order/biz/refund"
	"shopnexus-server/internal/module/order/biz/workflow/fullfilment"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/shared/paginate"
)

// DisputeHandler implements DisputeBiz over the module core (admin dismiss
// drives the refund credit flow) and signals the order's FulfillmentWorkflow
// directly.
type DisputeHandler struct {
	*orderbase.Base

	common      commonbiz.CommonBizClient
	fulfillment fullfilment.FulfillmentWfClient
	refund      *refund.RefundHandler
}

func New(
	c *orderbase.Base,
	common commonbiz.CommonBizClient,
	fulfillment fullfilment.FulfillmentWfClient,
	refund *refund.RefundHandler,
) *DisputeHandler {
	return &DisputeHandler{c, common, fulfillment, refund}
}

// DisputeBiz covers refund disputes (seller-initiated, admin-resolved).
type DisputeBiz interface {
	ListRefundDisputes(
		ctx context.Context,
		params ListRefundDisputesParams,
	) (paginate.PaginateResult[ordermodel.RefundDispute], error)
	GetRefundDispute(ctx context.Context, params GetRefundDisputeParams) (ordermodel.RefundDispute, error)
	AdminUpholdDispute(ctx context.Context, params AdminDisputeDecisionParams) (ordermodel.RefundDispute, error)
	AdminDismissDispute(ctx context.Context, params AdminDisputeDecisionParams) (ordermodel.RefundDispute, error)
}
