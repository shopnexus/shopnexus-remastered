package sellerorder

import (
	"context"

	"shopnexus-server/internal/infras/locker"
	inventorybiz "shopnexus-server/internal/module/inventory/biz"
	orderbase "shopnexus-server/internal/module/order/biz/base"
	"shopnexus-server/internal/module/order/biz/refund"
	"shopnexus-server/internal/module/order/biz/workflow/fullfilment"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/shared/paginate"

	"github.com/google/uuid"
	restate "github.com/restatedev/sdk-go"
)

// SellerHandler implements SellerOrderBiz over the module core
// (reject refunds buyers via CreditFromSession).
type SellerHandler struct {
	*orderbase.Base

	inventory   inventorybiz.InventoryBizClient
	locker      locker.Client
	refund      *refund.RefundHandler
	fulfillment fullfilment.FulfillmentWfClient
}

func New(
	c *orderbase.Base,
	inventory inventorybiz.InventoryBizClient,
	locker locker.Client,
	refund *refund.RefundHandler,
	fulfillment fullfilment.FulfillmentWfClient,
) *SellerHandler {
	return &SellerHandler{c, inventory, locker, refund, fulfillment}
}

// SellerOrderBiz covers the seller's incoming pending items and confirmed orders.
type SellerOrderBiz interface {
	ListSellerPendingItems(
		ctx context.Context,
		params ListSellerPendingItemsParams,
	) (paginate.PaginateResult[ordermodel.OrderItem], error)
	ConfirmSellerPending(ctx context.Context, params ConfirmSellerPendingParams) (ConfirmSellerPendingResult, error)
	RejectSellerPending(ctx restate.Context, params RejectSellerPendingParams) error
	GetSellerOrder(ctx context.Context, orderID uuid.UUID) (ordermodel.Order, error)
	ListSellerConfirmed(
		ctx context.Context,
		params ListSellerConfirmedParams,
	) (paginate.PaginateResult[ordermodel.Order], error)
}

// GetSellerOrder returns a single order by ID (seller perspective).
// TODO: add casbin authorization — verify caller is this order's seller
func (b *SellerHandler) GetSellerOrder(ctx context.Context, orderID uuid.UUID) (ordermodel.Order, error) {
	return b.GetHydratedOrder(ctx, orderID)
}
