package buyerorder

import (
	"fmt"

	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	restate "github.com/restatedev/sdk-go"
	"github.com/samber/lo"
)

type ListBuyerPendingOrdersParams struct {
	paginate.Params

	BuyerID uuid.UUID `validate:"required"`
}

// ListBuyerPendingOrders returns orders that are post-confirm but neither
// completed (payout released) nor cancelled. Includes orders awaiting
// shipment, in transit, delivered-but-not-paid-out.
func (b *BuyerHandler) ListBuyerPendingOrders(
	ctx restate.Context,
	params ListBuyerPendingOrdersParams,
) (paginate.PaginateResult[ordermodel.Order], error) {
	if err := validator.Validate(params); err != nil {
		return paginate.PaginateResult[ordermodel.Order]{}, fmt.Errorf(
			"validate list buyer pending orders params: %w",
			err,
		)
	}
	return b.listBuyerOrders(
		ctx,
		params.Params,
		params.BuyerID,
		func(rctx restate.Context, p orderListPage) ([]orderdb.OrderOrder, int64, error) {
			rows, err := b.Storage.Querier().ListBuyerPendingOrders(rctx, orderdb.ListBuyerPendingOrdersParams{
				BuyerID: p.BuyerID,
				Limit:   p.Limit,
				Offset:  p.Offset,
			})
			if err != nil {
				return nil, 0, err
			}
			orders := lo.Map(
				rows,
				func(r orderdb.ListBuyerPendingOrdersRow, _ int) orderdb.OrderOrder { return r.OrderOrder },
			)
			var total int64
			if len(rows) > 0 {
				total = rows[0].TotalCount
			}
			return orders, total, nil
		},
	)
}

type ListBuyerCompletedOrdersParams struct {
	paginate.Params

	BuyerID uuid.UUID `validate:"required"`
}

// ListBuyerCompletedOrders returns orders whose seller payout has been
// released (escrow done). Delivered-but-not-paid-out orders stay Pending.
func (b *BuyerHandler) ListBuyerCompletedOrders(
	ctx restate.Context,
	params ListBuyerCompletedOrdersParams,
) (paginate.PaginateResult[ordermodel.Order], error) {
	if err := validator.Validate(params); err != nil {
		return paginate.PaginateResult[ordermodel.Order]{}, fmt.Errorf(
			"validate list buyer completed orders params: %w",
			err,
		)
	}
	return b.listBuyerOrders(
		ctx,
		params.Params,
		params.BuyerID,
		func(rctx restate.Context, p orderListPage) ([]orderdb.OrderOrder, int64, error) {
			rows, err := b.Storage.Querier().ListBuyerCompletedOrders(rctx, orderdb.ListBuyerCompletedOrdersParams{
				BuyerID: p.BuyerID,
				Limit:   p.Limit,
				Offset:  p.Offset,
			})
			if err != nil {
				return nil, 0, err
			}
			orders := lo.Map(
				rows,
				func(r orderdb.ListBuyerCompletedOrdersRow, _ int) orderdb.OrderOrder { return r.OrderOrder },
			)
			var total int64
			if len(rows) > 0 {
				total = rows[0].TotalCount
			}
			return orders, total, nil
		},
	)
}

type ListBuyerCancelledOrdersParams struct {
	paginate.Params

	BuyerID uuid.UUID `validate:"required"`
}

// ListBuyerCancelledOrders returns orders where any of confirm/transport/payout
// is in a Failed or Cancelled state.
func (b *BuyerHandler) ListBuyerCancelledOrders(
	ctx restate.Context,
	params ListBuyerCancelledOrdersParams,
) (paginate.PaginateResult[ordermodel.Order], error) {
	if err := validator.Validate(params); err != nil {
		return paginate.PaginateResult[ordermodel.Order]{}, fmt.Errorf(
			"validate list buyer cancelled orders params: %w",
			err,
		)
	}
	return b.listBuyerOrders(
		ctx,
		params.Params,
		params.BuyerID,
		func(rctx restate.Context, p orderListPage) ([]orderdb.OrderOrder, int64, error) {
			rows, err := b.Storage.Querier().ListBuyerCancelledOrders(rctx, orderdb.ListBuyerCancelledOrdersParams{
				BuyerID: p.BuyerID,
				Limit:   p.Limit,
				Offset:  p.Offset,
			})
			if err != nil {
				return nil, 0, err
			}
			orders := lo.Map(
				rows,
				func(r orderdb.ListBuyerCancelledOrdersRow, _ int) orderdb.OrderOrder { return r.OrderOrder },
			)
			var total int64
			if len(rows) > 0 {
				total = rows[0].TotalCount
			}
			return orders, total, nil
		},
	)
}

// orderListPage carries the per-page args into the per-query closure.
type orderListPage struct {
	BuyerID uuid.UUID
	Limit   null.Int32
	Offset  null.Int32
}

func (b *BuyerHandler) listBuyerOrders(
	ctx restate.Context,
	pagination paginate.Params,
	buyerID uuid.UUID,
	fetch func(restate.Context, orderListPage) ([]orderdb.OrderOrder, int64, error),
) (paginate.PaginateResult[ordermodel.Order], error) {
	var zero paginate.PaginateResult[ordermodel.Order]
	if err := validator.Validate(struct {
		BuyerID uuid.UUID `validate:"required"`
	}{BuyerID: buyerID}); err != nil {
		return zero, fmt.Errorf("validate list orders: %w", err)
	}

	orders, total, err := fetch(ctx, orderListPage{
		BuyerID: buyerID,
		Limit:   pagination.Limit,
		Offset:  pagination.Offset(),
	})
	if err != nil {
		return zero, fmt.Errorf("db list orders: %w", err)
	}

	data, err := b.HydrateOrders(ctx, orders)
	if err != nil {
		return zero, fmt.Errorf("hydrate orders: %w", err)
	}

	var totalVal null.Int64
	totalVal.SetValid(total)
	return paginate.PaginateResult[ordermodel.Order]{
		PageParams: pagination,
		Total:      totalVal,
		Data:       data,
	}, nil
}
