package sellerorder

import (
	"context"

	"shopnexus-server/internal/infras/locker"
	inventorybiz "shopnexus-server/internal/module/inventory/biz"
	"shopnexus-server/internal/module/order/biz/refund"
	wfbase "shopnexus-server/internal/module/order/biz/workflow/base"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/shared/paginate"

	"github.com/google/uuid"
	restate "github.com/restatedev/sdk-go"
)

// SellerHandler implements SellerOrderBiz over the workflow-shared core
// (reject refunds buyers via CreditFromSession).
type SellerHandler struct {
	*wfbase.Base

	inventory inventorybiz.InventoryBizClient
	locker    locker.Client
	refund    *refund.RefundHandler
}

func New(
	c *wfbase.Base,
	inventory inventorybiz.InventoryBizClient,
	locker locker.Client,
	refund *refund.RefundHandler,
) *SellerHandler {
	return &SellerHandler{c, inventory, locker, refund}
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

// GetSellerOrder returns a single order by ID (seller perspective).
// TODO: add casbin authorization — verify caller is this order's seller
func (b *SellerHandler) GetSellerOrder(ctx restate.Context, orderID uuid.UUID) (ordermodel.Order, error) {
	return b.GetHydratedOrder(ctx, orderID)
}
