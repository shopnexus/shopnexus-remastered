package refund

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	restate "github.com/restatedev/sdk-go"
	"github.com/samber/lo"

	commonbiz "shopnexus-server/internal/module/common/biz"
	commondb "shopnexus-server/internal/module/common/db/sqlc"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/validator"
)

type ListBuyerRefundsParams struct {
	paginate.Params

	BuyerID uuid.UUID `validate:"required"`
}

// ListBuyerRefunds returns paginated refunds owned by the requesting buyer.
func (b *RefundHandler) ListBuyerRefunds(
	ctx restate.Context,
	params ListBuyerRefundsParams,
) (paginate.PaginateResult[ordermodel.Refund], error) {
	var zero paginate.PaginateResult[ordermodel.Refund]
	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate list buyer refunds: %w", err)
	}

	rows, err := b.Storage.Querier().ListCountRefund(ctx, orderdb.ListCountRefundParams{
		AccountID: []uuid.UUID{params.BuyerID},
		Offset:    params.Offset(),
		Limit:     params.Limit,
	})
	if err != nil {
		return zero, fmt.Errorf("list buyer refunds: %w", err)
	}
	if len(rows) == 0 {
		return zero, nil
	}

	refunds, err := b.HydrateRefunds(
		ctx,
		lo.Map(rows, func(r orderdb.ListCountRefundRow, _ int) orderdb.OrderRefund { return r.OrderRefund })...,
	)
	if err != nil {
		return zero, fmt.Errorf("hydrate buyer refunds: %w", err)
	}

	return paginate.PaginateResult[ordermodel.Refund]{
		PageParams: params.Params,
		Data:       refunds,
		Total:      null.IntFrom(rows[0].TotalCount),
	}, nil
}

type ListSellerRefundsParams struct {
	paginate.Params

	SellerID uuid.UUID `validate:"required"`
}

// ListSellerRefunds returns refunds raised against orders the requesting seller fulfilled.
func (b *RefundHandler) ListSellerRefunds(
	ctx restate.Context,
	params ListSellerRefundsParams,
) (paginate.PaginateResult[ordermodel.Refund], error) {
	var zero paginate.PaginateResult[ordermodel.Refund]
	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate list seller refunds: %w", err)
	}
	rows, err := b.Storage.Querier().ListSellerRefunds(ctx, orderdb.ListSellerRefundsParams{
		SellerID: params.SellerID,
		Offset:   params.Offset(),
		Limit:    params.Limit,
	})
	if err != nil {
		return zero, fmt.Errorf("list seller refunds: %w", err)
	}
	if len(rows) == 0 {
		return zero, nil
	}

	// Map resources to refunds
	resourcesMap, err := b.common.GetResources(ctx, commonbiz.GetResourcesParams{
		RefType: commondb.CommonResourceRefTypeRefund,
		RefIDs:  lo.Map(rows, func(r orderdb.ListSellerRefundsRow, _ int) uuid.UUID { return r.OrderRefund.ID }),
	})
	if err != nil {
		return zero, fmt.Errorf("list refund resources: %w", err)
	}

	refunds := lo.Map(rows, func(r orderdb.ListSellerRefundsRow, _ int) ordermodel.Refund {
		return ordermodel.Refund{
			OrderRefund: r.OrderRefund,
			Resources:   resourcesMap[r.OrderRefund.ID],
		}
	})
	return paginate.PaginateResult[ordermodel.Refund]{
		PageParams: params.Params,
		Data:       refunds,
		Total:      null.IntFrom(rows[0].TotalCount),
	}, nil
}
