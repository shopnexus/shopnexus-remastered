package orderbiz

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	restate "github.com/restatedev/sdk-go"

	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	"shopnexus-server/internal/shared/validator"
)

// HasPurchasedProduct checks if an account has a successful order containing any of the given SKU IDs.
func (b *reviewHandler) HasPurchasedProduct(ctx restate.Context, params HasPurchasedProductParams) (bool, error) {
	if err := validator.Validate(params); err != nil {
		return false, fmt.Errorf("validate has purchased product: %w", err)
	}

	return b.storage.Querier().HasPurchasedSku(ctx, orderdb.HasPurchasedSkuParams{
		AccountID: params.AccountID,
		SkuIds:    params.SkuIDs,
	})
}

// ListReviewableOrders returns successful orders that contain items matching the given SKU IDs.
func (b *reviewHandler) ListReviewableOrders(
	ctx restate.Context,
	params ListReviewableOrdersParams,
) ([]ReviewableOrder, error) {
	if err := validator.Validate(params); err != nil {
		return nil, fmt.Errorf("validate list reviewable orders: %w", err)
	}

	orders, err := b.storage.Querier().ListSuccessOrdersBySkus(ctx, orderdb.ListSuccessOrdersBySkusParams{
		BuyerID: params.AccountID,
		SkuIds:  params.SkuIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("db list reviewable orders: %w", err)
	}

	result := make([]ReviewableOrder, len(orders))
	for i, o := range orders {
		result[i] = ReviewableOrder{
			ID:          o.ID,
			DateCreated: o.DateCreated,
		}
	}
	return result, nil
}

// ValidateOrderForReview checks if a specific order is eligible for review.
func (b *reviewHandler) ValidateOrderForReview(ctx restate.Context, params ValidateOrderForReviewParams) (bool, error) {
	if err := validator.Validate(params); err != nil {
		return false, fmt.Errorf("validate order for review: %w", err)
	}

	return b.storage.Querier().ValidateOrderForReview(ctx, orderdb.ValidateOrderForReviewParams{
		OrderID: params.OrderID,
		BuyerID: params.AccountID,
		SkuIds:  params.SkuIDs,
	})
}

type HasPurchasedProductParams struct {
	AccountID uuid.UUID   `json:"account_id" validate:"required"`
	SkuIDs    []uuid.UUID `json:"sku_ids"    validate:"required,min=1"`
}

type ListReviewableOrdersParams struct {
	AccountID uuid.UUID   `json:"account_id" validate:"required"`
	SkuIDs    []uuid.UUID `json:"sku_ids"    validate:"required,min=1"`
}

type ReviewableOrder struct {
	ID          uuid.UUID `json:"id"`
	DateCreated time.Time `json:"date_created"`
}

type ValidateOrderForReviewParams struct {
	AccountID uuid.UUID   `json:"account_id" validate:"required"`
	OrderID   uuid.UUID   `json:"order_id"   validate:"required"`
	SkuIDs    []uuid.UUID `json:"sku_ids"    validate:"required,min=1"`
}
