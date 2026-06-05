package orderbiz

import (
	"fmt"

	restate "github.com/restatedev/sdk-go"

	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/samber/lo"
)

// GetBuyerOrder returns a single order by ID with all items and payment details.
// TODO: add casbin authorization — verify caller owns this order
func (b *orderQueryHandler) GetBuyerOrder(ctx restate.Context, orderID uuid.UUID) (ordermodel.Order, error) {
	var zero ordermodel.Order

	order, err := b.storage.Querier().GetOrder(ctx, orderdb.GetOrderParams{
		ID: uuid.NullUUID{UUID: orderID, Valid: true},
	})
	if err != nil {
		return zero, fmt.Errorf("get order: %w", err)
	}

	hydrated, err := b.hydrateOrders(ctx, []orderdb.OrderOrder{order})
	if err != nil {
		return zero, fmt.Errorf("hydrate order: %w", err)
	}
	if len(hydrated) == 0 {
		return zero, ordermodel.ErrOrderNotFound
	}

	return hydrated[0], nil
}

// GetSellerOrder returns a single order by ID (seller perspective).
// TODO: add casbin authorization — verify caller is this order's seller
func (b *orderQueryHandler) GetSellerOrder(ctx restate.Context, orderID uuid.UUID) (ordermodel.Order, error) {
	return b.GetBuyerOrder(ctx, orderID)
}

// ListSellerConfirmed returns paginated orders for the seller with optional payment/order status filters.
func (b *orderQueryHandler) ListSellerConfirmed(
	ctx restate.Context,
	params ListSellerConfirmedParams,
) (paginate.PaginateResult[ordermodel.Order], error) {
	var zero paginate.PaginateResult[ordermodel.Order]

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate list seller orders: %w", err)
	}

	listCountOrder, err := b.storage.Querier().ListCountSellerOrder(ctx, orderdb.ListCountSellerOrderParams{
		SellerID: params.SellerID,
		Search:   params.Search,
		Offset:   params.Offset(),
		Limit:    params.Limit,
	})
	if err != nil {
		return zero, fmt.Errorf("db list seller orders: %w", err)
	}

	var total null.Int64
	if len(listCountOrder) > 0 {
		total.SetValid(listCountOrder[0].TotalCount)
	}

	orders, err := b.hydrateOrders(
		ctx,
		lo.Map(listCountOrder, func(item orderdb.ListCountSellerOrderRow, _ int) orderdb.OrderOrder {
			return item.OrderOrder
		}),
	)
	if err != nil {
		return zero, fmt.Errorf("hydrate seller orders: %w", err)
	}

	return paginate.PaginateResult[ordermodel.Order]{
		PageParams: params.Params,
		Total:      total,
		Data:       orders,
	}, nil
}

type ListSellerConfirmedParams struct {
	paginate.Params

	SellerID uuid.UUID   `validate:"required"`
	Search   null.String `validate:"omitnil"`
}
