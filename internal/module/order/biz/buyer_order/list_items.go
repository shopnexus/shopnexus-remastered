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

type ListBuyerCancelledItemsParams struct {
	paginate.Params

	AccountID uuid.UUID `validate:"required"`
}

// ListBuyerCancelledItems returns pre-confirm items that died before becoming
// orders: failed/cancelled checkout sessions, or individually-refunded items
// from a Success session (date_cancelled set).
func (b *BuyerHandler) ListBuyerCancelledItems(
	ctx restate.Context,
	params ListBuyerCancelledItemsParams,
) (paginate.PaginateResult[ordermodel.OrderItem], error) {
	var zero paginate.PaginateResult[ordermodel.OrderItem]
	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate list cancelled items: %w", err)
	}
	return b.listBuyerItems(ctx, params.Params, params.AccountID,
		func(rctx restate.Context, accountID uuid.UUID) ([]orderdb.OrderItem, int64, error) {
			items, err := b.Storage.Querier().ListBuyerCancelledItems(rctx, accountID)
			if err != nil {
				return nil, 0, err
			}
			total, err := b.Storage.Querier().CountBuyerCancelledItems(rctx, accountID)
			if err != nil {
				return nil, 0, err
			}
			return items, total, nil
		})
}

// listBuyerItems is the shared backbone for buyer item-list endpoints.
// Mirrors the existing ListBuyerPendingItems shape including session attach.
func (b *BuyerHandler) listBuyerItems(
	ctx restate.Context,
	pagination paginate.Params,
	accountID uuid.UUID,
	fetch func(restate.Context, uuid.UUID) ([]orderdb.OrderItem, int64, error),
) (paginate.PaginateResult[ordermodel.OrderItem], error) {
	var zero paginate.PaginateResult[ordermodel.OrderItem]

	items, total, err := fetch(ctx, accountID)
	if err != nil {
		return zero, fmt.Errorf("db list items: %w", err)
	}

	enriched, err := b.EnrichItems(ctx, items)
	if err != nil {
		return zero, fmt.Errorf("enrich items: %w", err)
	}

	if len(enriched) > 0 {
		sessionIDs := lo.Uniq(
			lo.Map(enriched, func(it ordermodel.OrderItem, _ int) uuid.UUID { return it.PaymentSessionID }),
		)
		var sessionsRes paginate.PaginateResult[orderdb.OrderPaymentSession]
		sessionsRes, err = b.Storage.Querier().ListPaymentSession(ctx, orderdb.ListPaymentSessionParams{Id: sessionIDs})
		if err != nil {
			return zero, fmt.Errorf("db fetch payment sessions: %w", err)
		}
		sessions := sessionsRes.Data
		sessionMap := lo.KeyBy(sessions, func(s orderdb.OrderPaymentSession) uuid.UUID { return s.ID })
		for i := range enriched {
			if s, ok := sessionMap[enriched[i].PaymentSessionID]; ok {
				enriched[i].PaymentSession = &s
			}
		}
	}

	var totalVal null.Int64
	totalVal.SetValid(total)
	return paginate.PaginateResult[ordermodel.OrderItem]{
		PageParams: pagination,
		Total:      totalVal,
		Data:       enriched,
	}, nil
}

type ListBuyerPendingItemsParams struct {
	paginate.Params

	AccountID uuid.UUID `validate:"required"`
}

// ListBuyerPendingItems returns paginated paid pending items for the buyer.
func (b *BuyerHandler) ListBuyerPendingItems(
	ctx restate.Context,
	params ListBuyerPendingItemsParams,
) (paginate.PaginateResult[ordermodel.OrderItem], error) {
	if err := validator.Validate(params); err != nil {
		return paginate.PaginateResult[ordermodel.OrderItem]{}, fmt.Errorf("validate list pending items: %w", err)
	}
	return b.listBuyerItems(ctx, params.Params, params.AccountID,
		func(rctx restate.Context, accountID uuid.UUID) ([]orderdb.OrderItem, int64, error) {
			items, err := b.Storage.Querier().ListBuyerPendingItems(rctx, accountID)
			if err != nil {
				return nil, 0, err
			}
			total, err := b.Storage.Querier().CountBuyerPendingItems(rctx, accountID)
			if err != nil {
				return nil, 0, err
			}
			return items, total, nil
		})
}
