package dispute

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/samber/lo"

	accountmodel "shopnexus-server/internal/module/account/model"
	commonbiz "shopnexus-server/internal/module/common/biz"
	commondb "shopnexus-server/internal/module/common/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
	orderrepo "shopnexus-server/internal/module/order/repo"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/validator"
)

type ListRefundDisputesParams struct {
	paginate.Params

	Account  accountmodel.AuthenticatedAccount
	RefundID uuid.NullUUID         `validate:"omitnil"`
	Status   ordermodel.DisputeStatus `validate:"omitempty,validateFn=Valid"`
}

// ListRefundDisputes powers the admin queue and buyer/seller visibility.
//   - Admin caller: returns every dispute (optionally filtered by status / refund_id).
//   - Non-admin caller: only disputes attached to refunds the caller owns
//     (buyer of the refund, or seller of the order).
func (b *DisputeHandler) ListRefundDisputes(
	ctx context.Context,
	params ListRefundDisputesParams,
) (paginate.PaginateResult[ordermodel.RefundDispute], error) {
	var zero paginate.PaginateResult[ordermodel.RefundDispute]
	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate list disputes: %w", err)
	}

	var (
		statusFilter null.String
		refundFilter uuid.NullUUID
		buyerFilter  uuid.NullUUID
		sellerFilter uuid.NullUUID
	)
	if params.Status != "" {
		statusFilter = null.StringFrom(string(params.Status))
	}
	if params.RefundID.Valid {
		refundFilter = params.RefundID
	}
	if !params.Account.IsAdmin() {
		buyerFilter = uuid.NullUUID{UUID: params.Account.ID, Valid: true}
		sellerFilter = uuid.NullUUID{UUID: params.Account.ID, Valid: true}
	}

	rows, err := b.Storage.Querier().ListRefundDisputes(ctx, orderrepo.ListRefundDisputesParams{
		Status:         statusFilter,
		RefundID:       refundFilter,
		CallerBuyerID:  buyerFilter,
		CallerSellerID: sellerFilter,
		Offset:         params.Offset(),
		Limit:          params.Limit,
	})
	if err != nil {
		return zero, fmt.Errorf("list disputes: %w", err)
	}
	if len(rows) == 0 {
		return zero, nil
	}

	// Map resources to disputes
	resourcesMap, err := b.common.GetResources(ctx, commonbiz.GetResourcesParams{
		RefType: commondb.CommonResourceRefTypeRefundDispute,
		RefIDs:  lo.Map(rows, func(r ordermodel.WithTotal[ordermodel.RefundDispute], _ int) uuid.UUID { return r.Row.ID }),
	})
	if err != nil {
		return zero, fmt.Errorf("list dispute resources: %w", err)
	}

	disputes := lo.Map(rows, func(r ordermodel.WithTotal[ordermodel.RefundDispute], _ int) ordermodel.RefundDispute {
		r.Row.Resources = resourcesMap[r.Row.ID]
		return r.Row
	})
	return paginate.PaginateResult[ordermodel.RefundDispute]{
		PageParams: params.Params,
		Data:       disputes,
		Total:      null.IntFrom(rows[0].TotalCount),
	}, nil
}

type GetRefundDisputeParams struct {
	Account   accountmodel.AuthenticatedAccount
	DisputeID uuid.UUID `validate:"required"`
}

// GetRefundDispute returns a single dispute. Caller must be admin OR the
// buyer/seller attached to the underlying refund.
func (b *DisputeHandler) GetRefundDispute(
	ctx context.Context,
	params GetRefundDisputeParams,
) (ordermodel.RefundDispute, error) {
	var zero ordermodel.RefundDispute
	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate get dispute: %w", err)
	}

	dbDispute, err := b.Storage.Querier().GetRefundDispute(ctx, params.DisputeID)
	if err != nil {
		return zero, ordermodel.ErrDisputeNotFound
	}
	refund, err := b.Storage.Querier().GetRefund(ctx, dbDispute.RefundID)
	if err != nil {
		return zero, fmt.Errorf("get refund: %w", err)
	}
	order, err := b.Storage.Querier().GetOrder(ctx, refund.OrderID)
	if err != nil {
		return zero, fmt.Errorf("get order: %w", err)
	}
	if !params.Account.IsAdmin() &&
		params.Account.ID != refund.AccountID &&
		params.Account.ID != order.SellerID {
		return zero, ordermodel.ErrDisputeNotAuthorized
	}

	disputes, err := b.HydrateRefundDisputes(ctx, dbDispute)
	if err != nil {
		return zero, fmt.Errorf("hydrate dispute: %w", err)
	}
	return disputes[0], nil
}
