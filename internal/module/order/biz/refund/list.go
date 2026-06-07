package refund

import (
	"fmt"

	"github.com/google/uuid"
	restate "github.com/restatedev/sdk-go"

	"shopnexus-server/internal/module/order/biz/base"
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
	pagination := params.Params.Constrain()
	rows, err := b.Storage.Querier().ListBuyerRefunds(ctx, orderdb.ListBuyerRefundsParams{
		AccountID:   params.BuyerID,
		OffsetCount: pagination.Offset().Int32,
		LimitCount:  pagination.Limit.Int32,
	})
	if err != nil {
		return zero, fmt.Errorf("list buyer refunds: %w", err)
	}
	data := make([]ordermodel.Refund, 0, len(rows))
	for _, r := range rows {
		data = append(data, base.MapRefund(r))
	}
	return paginate.PaginateResult[ordermodel.Refund]{PageParams: pagination, Data: data}, nil
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
	pagination := params.Params.Constrain()
	rows, err := b.Storage.Querier().ListSellerRefunds(ctx, orderdb.ListSellerRefundsParams{
		SellerID:    params.SellerID,
		OffsetCount: pagination.Offset().Int32,
		LimitCount:  pagination.Limit.Int32,
	})
	if err != nil {
		return zero, fmt.Errorf("list seller refunds: %w", err)
	}
	data := make([]ordermodel.Refund, 0, len(rows))
	for _, r := range rows {
		data = append(data, base.MapRefund(r))
	}
	return paginate.PaginateResult[ordermodel.Refund]{PageParams: pagination, Data: data}, nil
}
