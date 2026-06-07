package dispute

import (
	"context"

	wfbase "shopnexus-server/internal/module/order/biz/workflow/base"
	"shopnexus-server/internal/module/order/biz/workflow/fullfilment"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/shared/paginate"
)

// DisputeHandler implements DisputeBiz over the workflow-shared core (admin
// dismiss drives the refund credit flow) and signals the order's
// FulfillmentWorkflow directly.
type DisputeHandler struct {
	*wfbase.Base

	fulfillment fullfilment.FulfillmentWfClient
}

func New(c *wfbase.Base, fulfillment fullfilment.FulfillmentWfClient) *DisputeHandler {
	return &DisputeHandler{c, fulfillment}
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
