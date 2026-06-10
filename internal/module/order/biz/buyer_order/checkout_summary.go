package buyerorder

import (
	"context"
	"fmt"

	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
)

type GetCheckoutSummaryParams struct {
	AccountID uuid.UUID `validate:"required"`
	TxID      uuid.UUID `validate:"required"`
}

func (b *BuyerHandler) GetCheckoutSummary(
	ctx context.Context,
	params GetCheckoutSummaryParams,
) (ordermodel.CheckoutSummary, error) {
	var zero ordermodel.CheckoutSummary

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate checkout summary: %w", err)
	}

	tx, err := b.Storage.Querier().GetTransaction(ctx, uuid.NullUUID{UUID: params.TxID, Valid: true})
	if err != nil {
		return zero, fmt.Errorf("get transaction: %w", err)
	}

	session, err := b.Storage.Querier().GetPaymentSession(ctx, uuid.NullUUID{UUID: tx.SessionID, Valid: true})
	if err != nil {
		return zero, fmt.Errorf("get payment session: %w", err)
	}

	dbItems, err := b.Storage.Querier().ListItemsByPaymentSession(ctx, session.ID)
	if err != nil {
		return zero, fmt.Errorf("list session items: %w", err)
	}

	// Authorize: only the owner may read the summary.
	for _, it := range dbItems {
		if it.AccountID != params.AccountID {
			return zero, ordermodel.ErrOrderItemNotFound
		}
	}

	enriched, err := b.EnrichItems(ctx, dbItems)
	if err != nil {
		return zero, fmt.Errorf("enrich items: %w", err)
	}

	items := make([]ordermodel.CheckoutSummaryItem, 0, len(enriched))
	for _, it := range enriched {
		items = append(items, ordermodel.CheckoutSummaryItem{
			ID:          it.ID,
			SkuID:       it.SkuID,
			SpuID:       it.SpuID,
			Slug:        it.Slug,
			SkuName:     it.SkuName,
			Quantity:    it.Quantity,
			TotalAmount: it.TotalAmount,
			Currency:    session.Currency,
			ImageURL:    it.ImageURL,
		})
	}

	return ordermodel.CheckoutSummary{
		Session: session,
		Items:   items,
	}, nil
}
