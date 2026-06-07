package buyerorder

import (
	"context"

	accountbiz "shopnexus-server/internal/module/account/biz"
	inventorybiz "shopnexus-server/internal/module/inventory/biz"
	"shopnexus-server/internal/module/order/biz/base"
	"shopnexus-server/internal/module/order/biz/workflow/checkout"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/shared/paginate"

	"github.com/google/uuid"
	restate "github.com/restatedev/sdk-go"
)

// BuyerHandler implements BuyerOrderBiz over the shared core and signals the
// buyer's CheckoutWorkflow directly.
type BuyerHandler struct {
	*base.Base

	account   accountbiz.AccountBizClient
	inventory inventorybiz.InventoryBizClient
	checkout  checkout.CheckoutWfClient
}

func New(
	c *base.Base,
	account accountbiz.AccountBizClient,
	inventory inventorybiz.InventoryBizClient,
	checkout checkout.CheckoutWfClient,
) *BuyerHandler {
	return &BuyerHandler{c, account, inventory, checkout}
}

// BuyerOrderBiz covers the buyer's view of pending items and confirmed orders.
type BuyerOrderBiz interface {
	ListBuyerPendingItems(
		ctx context.Context,
		params ListBuyerPendingItemsParams,
	) (paginate.PaginateResult[ordermodel.OrderItem], error)
	CancelBuyerPending(ctx context.Context, params CancelBuyerPendingParams) error
	RefundPendingItem(ctx context.Context, params RefundPendingItemParams) error
	ListBuyerPendingOrders(
		ctx context.Context,
		params ListBuyerPendingOrdersParams,
	) (paginate.PaginateResult[ordermodel.Order], error)
	ListBuyerCompletedOrders(
		ctx context.Context,
		params ListBuyerCompletedOrdersParams,
	) (paginate.PaginateResult[ordermodel.Order], error)
	ListBuyerCancelledOrders(
		ctx context.Context,
		params ListBuyerCancelledOrdersParams,
	) (paginate.PaginateResult[ordermodel.Order], error)
	ListBuyerCancelledItems(
		ctx context.Context,
		params ListBuyerCancelledItemsParams,
	) (paginate.PaginateResult[ordermodel.OrderItem], error)
	GetBuyerOrder(ctx context.Context, orderID uuid.UUID) (ordermodel.Order, error)
	GetCheckoutSummary(ctx context.Context, params GetCheckoutSummaryParams) (ordermodel.CheckoutSummary, error)
}

// GetBuyerOrder returns a single order by ID with all items and payment details.
// TODO: add casbin authorization — verify caller owns this order
func (b *BuyerHandler) GetBuyerOrder(ctx restate.Context, orderID uuid.UUID) (ordermodel.Order, error) {
	return b.GetHydratedOrder(ctx, orderID)
}
