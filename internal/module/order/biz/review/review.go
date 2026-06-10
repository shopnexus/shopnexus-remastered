package review

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/samber/lo"

	accountmodel "shopnexus-server/internal/module/account/model"
	catalogbiz "shopnexus-server/internal/module/catalog/biz"
	catalogdb "shopnexus-server/internal/module/catalog/db/sqlc"
	catalogmodel "shopnexus-server/internal/module/catalog/model"
	"shopnexus-server/internal/module/order/biz/base"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	"shopnexus-server/internal/shared/validator"
)

// ReviewHandler implements ReviewBiz over the shared core. It is the review
// entry point: purchase eligibility is validated here against local order
// data, then the review is forwarded to the catalog store.
type ReviewHandler struct {
	*base.Base

	catalog catalogbiz.CatalogBizClient
}

func New(c *base.Base, catalog catalogbiz.CatalogBizClient) *ReviewHandler {
	return &ReviewHandler{c, catalog}
}

// ReviewBiz covers product-review eligibility checks and review creation.
type ReviewBiz interface {
	HasPurchasedProduct(ctx context.Context, params HasPurchasedProductParams) (bool, error)
	ListReviewableOrders(ctx context.Context, params ListReviewableOrdersParams) ([]ReviewableOrder, error)
	ListReviewableOrdersBySpu(ctx context.Context, params ListReviewableOrdersBySpuParams) ([]ReviewableOrder, error)
	ValidateOrderForReview(ctx context.Context, params ValidateOrderForReviewParams) (bool, error)
	CreateProductReview(ctx context.Context, params CreateProductReviewParams) (catalogmodel.Comment, error)
}

type HasPurchasedProductParams struct {
	AccountID uuid.UUID   `validate:"required"`
	SkuIDs    []uuid.UUID `validate:"required,min=1"`
}

// HasPurchasedProduct checks if an account has a successful order containing any of the given SKU IDs.
func (b *ReviewHandler) HasPurchasedProduct(ctx context.Context, params HasPurchasedProductParams) (bool, error) {
	if err := validator.Validate(params); err != nil {
		return false, fmt.Errorf("validate has purchased product: %w", err)
	}

	return b.Storage.Querier().HasPurchasedSku(ctx, orderdb.HasPurchasedSkuParams{
		AccountID: params.AccountID,
		SkuIds:    params.SkuIDs,
	})
}

type ListReviewableOrdersParams struct {
	AccountID uuid.UUID   `validate:"required"`
	SkuIDs    []uuid.UUID `validate:"required,min=1"`
}

type ReviewableOrder struct {
	ID          uuid.UUID `json:"id"`
	DateCreated time.Time `json:"date_created"`
}

// ListReviewableOrders returns successful orders that contain items matching the given SKU IDs.
func (b *ReviewHandler) ListReviewableOrders(
	ctx context.Context,
	params ListReviewableOrdersParams,
) ([]ReviewableOrder, error) {
	if err := validator.Validate(params); err != nil {
		return nil, fmt.Errorf("validate list reviewable orders: %w", err)
	}

	orders, err := b.Storage.Querier().ListSuccessOrdersBySkus(ctx, orderdb.ListSuccessOrdersBySkusParams{
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

type ValidateOrderForReviewParams struct {
	AccountID uuid.UUID   `validate:"required"`
	OrderID   uuid.UUID   `validate:"required"`
	SkuIDs    []uuid.UUID `validate:"required,min=1"`
}

// ValidateOrderForReview checks if a specific order is eligible for review.
func (b *ReviewHandler) ValidateOrderForReview(ctx context.Context, params ValidateOrderForReviewParams) (bool, error) {
	if err := validator.Validate(params); err != nil {
		return false, fmt.Errorf("validate order for review: %w", err)
	}

	return b.Storage.Querier().ValidateOrderForReview(ctx, orderdb.ValidateOrderForReviewParams{
		OrderID: params.OrderID,
		BuyerID: params.AccountID,
		SkuIds:  params.SkuIDs,
	})
}

// spuSkuIDs resolves all SKU IDs of an SPU via the catalog module.
func (b *ReviewHandler) spuSkuIDs(ctx context.Context, spuID uuid.UUID) ([]uuid.UUID, error) {
	skus, err := b.catalog.ListProductSku(ctx, catalogbiz.ListProductSkuParams{
		SpuID: []uuid.UUID{spuID},
	})
	if err != nil {
		return nil, fmt.Errorf("list product skus: %w", err)
	}
	if len(skus) == 0 {
		return nil, catalogmodel.ErrProductNotFound
	}
	return lo.Map(skus, func(sku catalogmodel.ProductSku, _ int) uuid.UUID { return sku.ID }), nil
}

type ListReviewableOrdersBySpuParams struct {
	Account accountmodel.AuthenticatedAccount
	SpuID   uuid.UUID `validate:"required"`
}

// ListReviewableOrdersBySpu returns completed orders for a product that the
// user can review.
func (b *ReviewHandler) ListReviewableOrdersBySpu(
	ctx context.Context,
	params ListReviewableOrdersBySpuParams,
) ([]ReviewableOrder, error) {
	if err := validator.Validate(params); err != nil {
		return nil, fmt.Errorf("validate list reviewable orders by spu: %w", err)
	}

	skuIDs, err := b.spuSkuIDs(ctx, params.SpuID)
	if err != nil {
		return nil, err
	}
	return b.ListReviewableOrders(ctx, ListReviewableOrdersParams{
		AccountID: params.Account.ID,
		SkuIDs:    skuIDs,
	})
}

type CreateProductReviewParams struct {
	Account accountmodel.AuthenticatedAccount

	SpuID   uuid.UUID `validate:"required"`
	OrderID uuid.UUID `validate:"required"`
	Body    string    `validate:"required,min=1,max=1000"`
	Score   float64   `validate:"required,gte=0,lte=1"`

	ResourceIDs []uuid.UUID `validate:"omitempty,dive"`
}

// CreateProductReview validates purchase eligibility against local order data,
// then forwards the review to the catalog store with denormalized order facts
// (item name + order date) so catalog never calls back into order.
func (b *ReviewHandler) CreateProductReview(
	ctx context.Context,
	params CreateProductReviewParams,
) (catalogmodel.Comment, error) {
	var zero catalogmodel.Comment

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate create product review: %w", err)
	}

	skuIDs, err := b.spuSkuIDs(ctx, params.SpuID)
	if err != nil {
		return zero, err
	}

	valid, err := b.Storage.Querier().ValidateOrderForReview(ctx, orderdb.ValidateOrderForReviewParams{
		OrderID: params.OrderID,
		BuyerID: params.Account.ID,
		SkuIds:  skuIDs,
	})
	if err != nil {
		return zero, fmt.Errorf("validate order for review: %w", err)
	}
	if !valid {
		return zero, catalogmodel.ErrMustPurchaseToReview
	}

	return b.catalog.Call().CreateComment(ctx, catalogbiz.CreateCommentParams{
		Account:     params.Account,
		RefType:     catalogdb.CatalogCommentRefTypeProductSpu,
		RefID:       params.SpuID,
		Body:        params.Body,
		Score:       params.Score,
		OrderID:     params.OrderID,
		ResourceIDs: params.ResourceIDs,
	})
}
