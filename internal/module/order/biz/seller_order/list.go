package sellerorder

import (
	"context"
	"fmt"

	ordermodel "shopnexus-server/internal/module/order/model"
	orderrepo "shopnexus-server/internal/module/order/repo"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/samber/lo"
)

type ListSellerPendingItemsParams struct {
	paginate.Params

	SellerID uuid.UUID `validate:"required"`
}

// ListSellerPendingItems returns paginated pending items for the seller.
func (b *SellerHandler) ListSellerPendingItems(
	ctx context.Context,
	params ListSellerPendingItemsParams,
) (paginate.PaginateResult[ordermodel.OrderItem], error) {
	var zero paginate.PaginateResult[ordermodel.OrderItem]

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate list seller pending items params: %w", err)
	}

	items, err := b.Storage.Querier().ListSellerPendingItems(ctx, params.SellerID)
	if err != nil {
		return zero, fmt.Errorf("db list seller pending items: %w", err)
	}
	total, err := b.Storage.Querier().CountSellerPendingItems(ctx, params.SellerID)
	if err != nil {
		return zero, fmt.Errorf("db count seller pending items: %w", err)
	}

	enriched, err := b.EnrichItems(ctx, items)
	if err != nil {
		return zero, fmt.Errorf("enrich seller pending items: %w", err)
	}

	var totalVal null.Int64
	totalVal.SetValid(total)

	return paginate.PaginateResult[ordermodel.OrderItem]{
		PageParams: params.Params,
		Total:      totalVal,
		Data:       enriched,
	}, nil
}

type ListSellerConfirmedParams struct {
	paginate.Params

	SellerID uuid.UUID   `validate:"required"`
	Search   null.String `validate:"omitnil"`
}

// ListSellerConfirmed returns paginated orders for the seller with optional payment/order status filters.
func (b *SellerHandler) ListSellerConfirmed(
	ctx context.Context,
	params ListSellerConfirmedParams,
) (paginate.PaginateResult[ordermodel.Order], error) {
	var zero paginate.PaginateResult[ordermodel.Order]

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate list seller orders: %w", err)
	}

	listCountOrder, err := b.Storage.Querier().ListCountSellerOrder(ctx, orderrepo.ListCountSellerOrderParams{
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

	orders, err := b.HydrateOrders(
		ctx,
		lo.Map(listCountOrder, func(item ordermodel.WithTotal[ordermodel.Order], _ int) ordermodel.Order {
			return item.Row
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
