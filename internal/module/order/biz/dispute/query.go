package dispute

import (
	"fmt"

	"github.com/google/uuid"
	restate "github.com/restatedev/sdk-go"

	accountmodel "shopnexus-server/internal/module/account/model"
	"shopnexus-server/internal/module/order/biz/base"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/validator"
)

type ListRefundDisputesParams struct {
	paginate.Params

	Account  accountmodel.AuthenticatedAccount
	RefundID uuid.NullUUID              `validate:"omitnil"`
	Status   orderdb.OrderDisputeStatus `validate:"omitempty,validateFn=Valid"`
}

// ListRefundDisputes powers the admin queue and buyer/seller visibility.
//   - Admin caller: returns every dispute (optionally filtered by status / refund_id).
//   - Non-admin caller: only disputes attached to refunds the caller owns
//     (buyer of the refund, or seller of the order).
func (b *DisputeHandler) ListRefundDisputes(
	ctx restate.Context,
	params ListRefundDisputesParams,
) (paginate.PaginateResult[ordermodel.RefundDispute], error) {
	var zero paginate.PaginateResult[ordermodel.RefundDispute]
	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate list disputes: %w", err)
	}
	pagination := params.Params.Constrain()

	var (
		statusFilter orderdb.NullOrderDisputeStatus
		refundFilter uuid.NullUUID
		buyerFilter  uuid.NullUUID
		sellerFilter uuid.NullUUID
	)
	if params.Status != "" {
		statusFilter = orderdb.NullOrderDisputeStatus{
			OrderDisputeStatus: orderdb.OrderDisputeStatus(params.Status),
			Valid:              true,
		}
	}
	if params.RefundID.Valid {
		refundFilter = params.RefundID
	}
	if !params.Account.IsAdmin() {
		buyerFilter = uuid.NullUUID{UUID: params.Account.ID, Valid: true}
		sellerFilter = uuid.NullUUID{UUID: params.Account.ID, Valid: true}
	}

	rows, err := b.Storage.Querier().ListRefundDisputes(ctx, orderdb.ListRefundDisputesParams{
		Status:         statusFilter,
		RefundID:       refundFilter,
		CallerBuyerID:  buyerFilter,
		CallerSellerID: sellerFilter,
		OffsetCount:    pagination.Offset().Int32,
		LimitCount:     pagination.Limit.Int32,
	})
	if err != nil {
		return zero, fmt.Errorf("list disputes: %w", err)
	}

	data := make([]ordermodel.RefundDispute, 0, len(rows))
	for _, r := range rows {
		data = append(data, base.MapRefundDispute(r))
	}
	return paginate.PaginateResult[ordermodel.RefundDispute]{PageParams: pagination, Data: data}, nil
}

type GetRefundDisputeParams struct {
	Account   accountmodel.AuthenticatedAccount
	DisputeID uuid.UUID `validate:"required"`
}

// GetRefundDispute returns a single dispute. Caller must be admin OR the
// buyer/seller attached to the underlying refund.
func (b *DisputeHandler) GetRefundDispute(
	ctx restate.Context,
	params GetRefundDisputeParams,
) (ordermodel.RefundDispute, error) {
	var zero ordermodel.RefundDispute
	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate get dispute: %w", err)
	}

	dispute, err := b.Storage.Querier().GetRefundDispute(ctx, uuid.NullUUID{UUID: params.DisputeID, Valid: true})
	if err != nil {
		return zero, ordermodel.ErrDisputeNotFound
	}
	refund, err := b.Storage.Querier().GetRefund(ctx, orderdb.GetRefundParams{
		ID: uuid.NullUUID{UUID: dispute.RefundID, Valid: true},
	})
	if err != nil {
		return zero, fmt.Errorf("get refund: %w", err)
	}
	order, err := b.Storage.Querier().GetOrder(ctx, orderdb.GetOrderParams{
		ID: uuid.NullUUID{UUID: refund.OrderID, Valid: true},
	})
	if err != nil {
		return zero, fmt.Errorf("get order: %w", err)
	}
	if !params.Account.IsAdmin() &&
		params.Account.ID != refund.AccountID &&
		params.Account.ID != order.SellerID {
		return zero, ordermodel.ErrDisputeNotAuthorized
	}
	return base.MapRefundDispute(dispute), nil
}
