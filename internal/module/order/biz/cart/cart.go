package cart

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/guregu/null/v6"

	accountmodel "shopnexus-server/internal/module/account/model"
	analyticbiz "shopnexus-server/internal/module/analytic/biz"
	analyticmodel "shopnexus-server/internal/module/analytic/model"
	catalogbiz "shopnexus-server/internal/module/catalog/biz"
	catalogmodel "shopnexus-server/internal/module/catalog/model"
	commonbiz "shopnexus-server/internal/module/common/biz"
	commondb "shopnexus-server/internal/module/common/db/sqlc"
	commonmodel "shopnexus-server/internal/module/common/model"
	"shopnexus-server/internal/module/order/biz/base"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
	"github.com/samber/lo"
)

// CartHandler implements CartBiz over the shared core.
type CartHandler struct {
	*base.Base

	catalog catalogbiz.CatalogBizClient
	common  commonbiz.CommonBizClient
}

func New(
	c *base.Base,
	catalog catalogbiz.CatalogBizClient,
	common commonbiz.CommonBizClient,
) *CartHandler {
	return &CartHandler{c, catalog, common}
}

// CartBiz covers the buyer's shopping cart.
type CartBiz interface {
	GetCart(ctx context.Context, params GetCartParams) ([]ordermodel.CartItem, error)
	UpdateCart(ctx context.Context, params UpdateCartParams) error
	ClearCart(ctx context.Context, params ClearCartParams) error
}

type GetCartParams struct {
	AccountID uuid.UUID `validate:"required"`
}

// GetCart returns all cart items for the given account with SKU details and product images.
func (b *CartHandler) GetCart(ctx context.Context, params GetCartParams) ([]ordermodel.CartItem, error) {
	if err := validator.Validate(params); err != nil {
		return nil, fmt.Errorf("validate get cart params: %w", err)
	}
	cartItemsRes, err := b.Storage.Querier().ListCartItem(ctx, orderdb.ListCartItemParams{
		AccountId: []uuid.UUID{params.AccountID},
	})
	if err != nil {
		return nil, fmt.Errorf("db list cart items: %w", err)
	}
	cartItems := cartItemsRes.Data

	skus, err := b.catalog.Guaranteed().ListProductSku(ctx, catalogbiz.ListProductSkuParams{
		ID: lo.Map(cartItems, func(c orderdb.OrderCartItem, _ int) uuid.UUID { return c.SkuID }),
	})
	if err != nil {
		return nil, fmt.Errorf("list cart skus: %w", err)
	}
	skuMap := lo.SliceToMap(skus, func(s catalogmodel.ProductSku) (uuid.UUID, catalogmodel.ProductSku) {
		return s.ID, s
	})

	// Batch-fetch all SPU resources in a single call.
	spuIDs := lo.Uniq(lo.Map(skus, func(s catalogmodel.ProductSku, _ int) uuid.UUID { return s.SpuID }))
	resourcesMap, err := b.common.GetResources(ctx, commonbiz.GetResourcesParams{
		RefType: commondb.CommonResourceRefTypeProductSpu,
		RefIDs:  spuIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("get cart resources: %w", err)
	}

	listSpu, err := b.catalog.Guaranteed().ListProductSpu(ctx, catalogbiz.ListProductSpuParams{
		ID: spuIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("list cart spus: %w", err)
	}
	currencyMap := lo.SliceToMap(listSpu.Data, func(s catalogmodel.ProductSpu) (uuid.UUID, string) {
		return s.ID, s.Currency
	})

	items := make([]ordermodel.CartItem, 0, len(cartItems))
	for _, cartItem := range cartItems {
		sku := skuMap[cartItem.SkuID]

		var resource *commonmodel.Resource
		if res, exists := resourcesMap[sku.SpuID]; exists && len(res) > 0 {
			resource = &res[0]
		}

		items = append(items, ordermodel.CartItem{
			SpuID:    sku.SpuID,
			Sku:      sku,
			Quantity: cartItem.Quantity,
			Resource: resource,
			Currency: currencyMap[sku.SpuID],
		})
	}

	return items, nil
}

type UpdateCartParams struct {
	Account accountmodel.AuthenticatedAccount

	SkuID         uuid.UUID  `validate:"required"`
	Quantity      null.Int64 `validate:"omitnil,min=0,max=1000"`
	DeltaQuantity null.Int64 `validate:"omitnil,min=-1000,max=1000"`
}

// UpdateCart adds, updates, or removes a cart item and tracks the interaction.
func (b *CartHandler) UpdateCart(ctx context.Context, params UpdateCartParams) error {
	if err := validator.Validate(params); err != nil {
		return fmt.Errorf("validate update cart: %w", err)
	}

	// Track which event type to send after the durable step
	eventType, err := func() (analyticmodel.Event, error) {
		var newQuantity int64

		if params.DeltaQuantity.Valid {
			cartItem, err := b.Storage.Querier().GetCartItem(ctx, orderdb.GetCartItemParams{
				AccountID: uuid.NullUUID{UUID: params.Account.ID, Valid: true},
				SkuID:     uuid.NullUUID{UUID: params.SkuID, Valid: true},
			})
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return "", err
			}
			newQuantity = cartItem.Quantity + params.DeltaQuantity.Int64
		} else if params.Quantity.Valid {
			newQuantity = params.Quantity.Int64
		} else {
			return "", ordermodel.ErrQuantityParamRequired
		}

		// If quantity = 0, remove cart item and return early
		if params.Quantity.Valid && params.Quantity.Int64 <= 0 {
			if err := b.Storage.Querier().DeleteCartItem(ctx, orderdb.DeleteCartItemParams{
				AccountID: []uuid.UUID{params.Account.ID},
				SkuID:     []uuid.UUID{params.SkuID},
			}); err != nil {
				return "", err
			}
			return analyticmodel.EventRemoveFromCart, nil
		}

		if err := b.Storage.Querier().UpdateCart(ctx, orderdb.UpdateCartParams{
			AccountID: params.Account.ID,
			SkuID:     params.SkuID,
			Quantity:  newQuantity,
		}); err != nil {
			return "", err
		}
		return analyticmodel.EventAddToCart, nil
	}()
	if err != nil {
		return fmt.Errorf("db update cart: %w", err)
	}

	if err = b.TrackInteractions(ctx, analyticbiz.CreateInteraction{
		Account:   params.Account,
		EventType: eventType,
		RefType:   analyticmodel.InteractionRefTypeProduct,
		RefID:     params.SkuID.String(),
	}); err != nil {
		return fmt.Errorf("track interaction: %w", err)
	}
	return nil
}

type ClearCartParams struct {
	Account accountmodel.AuthenticatedAccount
}

// ClearCart removes all items from the account's cart.
func (b *CartHandler) ClearCart(ctx context.Context, params ClearCartParams) error {
	return b.Storage.Querier().DeleteCartItem(ctx, orderdb.DeleteCartItemParams{
		AccountID: []uuid.UUID{params.Account.ID},
	})
}
