package paymentbiz

import (
	"context"
	"fmt"
	"shopnexus-remastered/internal/db"
	sharedmodel "shopnexus-remastered/internal/module/shared/model"
	pgxsqlc "shopnexus-remastered/internal/utils/pgx/sqlc"
	"shopnexus-remastered/internal/utils/ptr"
)

type OrderBiz struct {
	storage *pgxsqlc.Storage
}

func NewOrderBiz(storage *pgxsqlc.Storage) *OrderBiz {
	return &OrderBiz{
		storage: storage,
	}
}

type GetOrderParams = struct {
	AccountID   int64
	AccountType db.AccountType
	OrderID     int64
}

func (s *OrderBiz) GetOrder(ctx context.Context, params GetOrderParams) (db.OrderBase, error) {
	storageParams := db.GetOrderBaseParams{
		ID: params.OrderID,
	}

	//if params.Role == db.RoleUser {
	//	storageParams.UserID = &params.AccountID
	//}

	return s.storage.GetOrderBase(ctx, storageParams)
}

type ListOrdersParams struct {
	sharedmodel.PaginationParams
}

func (s *OrderBiz) ListOrders(ctx context.Context, params ListOrdersParams) (result sharedmodel.PaginateResult[db.OrderOrder], err error) {
	storageParams := db.ListOrderParams{
		Limit:  params.GetLimit(),
		Offset: params.GetOffset(),
	}

	// User only see their own payments
	//if params.Role == db.RoleUser {
	//	storageParams.UserID = &params.AccountID
	//}

	total, err := s.storage.CountOrder(ctx, db.CountOrderParams{})
	if err != nil {
		return result, err
	}

	payments, err := s.storage.ListOrder(ctx, storageParams)
	if err != nil {
		return result, err
	}

	return sharedmodel.PaginateResult[db.OrderOrder]{
		Data:       payments,
		Limit:      params.Limit,
		Page:       params.Page,
		Total:      total,
		NextPage:   params.NextPage(total),
		NextCursor: params.NextCursor(payments[len(payments)-1].ID),
	}, nil
}

type CreateOrderParams struct {
	UserID        int64
	Address       string
	OrderMethod   db.OrderPaymentMethod
	ProductSkuIDs []int64
}

type CreateOrderResult struct {
	Order db.OrderOrder
	Url   string
}

func (s *OrderBiz) CreateOrder(ctx context.Context, params CreateOrderParams) (CreateOrderResult, error) {
	var zero CreateOrderResult

	// Start transaction
	txStorage, err := s.storage.BeginTx(ctx)
	if err != nil {
		return zero, err
	}
	defer txStorage.Rollback(ctx)

	// Remove products from cartItems
	if err = txStorage.RemoveCartItem(ctx, cartItems.ID, params.ProductSkuIDs); err != nil {
		return zero, err
	}

	var (
		productOnOrders []db.ProductOnOrder
		totalOrder      int64
	)

	// Calculate total payment
	// Iterate through each product model in the cartItems
	for _, cartProduct := range cartItems.Products {
		// Get product details
		product, err := txStorage.GetProduct(ctx, cartProduct.GetID())
		if err != nil {
			return CreateOrderResult{}, err
		}

		// Get product model details
		productModel, err := txStorage.GetProductModel(ctx, product.ProductModelID)
		if err != nil {
			return CreateOrderResult{}, err
		}

		// Get any available product serial_ids from that product
		var serialIDs []string
		productSerials, err := txStorage.GetAvailableProducts(
			ctx,
			cartProduct.GetID(),
			cartProduct.GetQuantity(),
		)
		if err != nil {
			return CreateOrderResult{}, err
		}
		for _, productSerial := range productSerials {
			serialIDs = append(serialIDs, productSerial.SerialID)
		}

		// Get available sales for the product model
		sales, err := txStorage.GetAvailableSales(ctx, db.GetLatestSaleParams{
			ProductModelID: productModel.ID,
			BrandID:        productModel.BrandID,
			Tags:           productModel.Tags,
		})
		if err != nil {
			return CreateOrderResult{}, err
		}

		combinePrice := (productModel.ListPrice + product.AdditionalPrice) * cartProduct.GetQuantity()
		var combineDiscount int64

		// Apply sales
		for _, sale := range sales {
			combineDiscount += sale.Apply(productModel.ListPrice+product.AdditionalPrice) * cartProduct.GetQuantity()
		}

		// Ensure combineDiscount is not greater than combinePrice
		if combineDiscount > combinePrice {
			combineDiscount = combinePrice
		}

		totalOrder += combinePrice - combineDiscount

		// If product can combine, add all quantity at once
		if product.CanCombine {
			productOnOrders = append(productOnOrders, db.ProductOnOrder{
				ItemQuantityBase: db.ItemQuantityBase[int64]{
					ItemID:   cartProduct.GetID(),
					Quantity: cartProduct.GetQuantity(),
				},
				SerialIDs:  serialIDs,
				Price:      combinePrice,
				TotalPrice: combinePrice - combineDiscount,
			})
		} else {
			for i := int64(0); i < cartProduct.GetQuantity(); i++ {
				productOnOrders = append(productOnOrders, db.ProductOnOrder{
					ItemQuantityBase: db.ItemQuantityBase[int64]{
						ItemID:   cartProduct.GetID(),
						Quantity: 1,
					},
					SerialIDs:  []string{serialIDs[i]},
					Price:      combinePrice / cartProduct.GetQuantity(),
					TotalPrice: (combinePrice - combineDiscount) / cartProduct.GetQuantity(),
				})
			}
		}
	}

	// Create payment
	newOrder, err := txStorage.CreateDefaultOrder(ctx, db.CreateDefaultOrderParams{
		//UserID:   params.UserID,
		//Address:  params.Address,
		//Method:   params.OrderMethod,
		//Total:    totalOrder,
		//Status:   db.StatusPending,
		//Products: productOnOrders,
	})
	if err != nil {
		return CreateOrderResult{}, err
	}

	// Create payment url
	var pp OrderPlatform

	switch params.OrderMethod {
	case db.OrderOrderMethodVnpay:
		pp, err = s.getPlatform(PlatformVNPAY)
		if err != nil {
			return CreateOrderResult{}, err
		}
	case db.OrderOrderMethodMomo:
		// TODO: support momo payment
		return CreateOrderResult{}, fmt.Errorf("payment method momo not yet supported")
		// pp, err = s.GetPlatform(PlatformMOMO)
		// if err != nil {
		// 	return CreateOrderResult{}, err
		// }
	case db.OrderOrderMethodCash:
		// Do nothing
		// TODO: add logic for cash payment
		return CreateOrderResult{}, fmt.Errorf("payment method cash not yet supported")
	default:
		return CreateOrderResult{}, fmt.Errorf("payment method %s not supported", params.OrderMethod)
	}

	url, err := pp.CreateOrder(ctx, CreateOrderParams{
		OrderID: newOrder.ID,
		Info:    fmt.Sprintf("Order for order %d", newOrder.ID),
		Amount:  newOrder.Total,
	})
	if err != nil {
		return CreateOrderResult{}, err
	}

	// TODO: move this update product sold to cron job check success payment (because currently we don't know if payment is success or not)
	txProductSvc, err := s.productSvc.WithTx(txStorage)
	if err != nil {
		return CreateOrderResult{}, err
	}
	if err = txProductSvc.UpdateProductSold(ctx, product.UpdateProductSoldParams{
		IDs: func() []int64 {
			ids := make([]int64, 0, len(productOnOrders))
			for _, pop := range productOnOrders {
				ids = append(ids, pop.ItemID)
			}
			return ids
		}(),
		Amount: 1,
	}); err != nil {
		return CreateOrderResult{}, err
	}

	// Rollback if purchase failed
	if err = txStorage.Commit(ctx); err != nil {
		return CreateOrderResult{}, err
	}

	return CreateOrderResult{Order: newOrder, Url: url}, nil
}

func (s *OrderBiz) getPlatform(platform Platform) (OrderPlatform, error) {
	pp, ok := s.platforms[platform]
	if !ok {
		return nil, fmt.Errorf("platform %s not found", platform)
	}
	return pp, nil
}

type UpdateOrderParams struct {
	ID        int64
	AccountID int64
	Role      db.AccountType
	Method    *db.OrderOrderMethod
	Address   *string
	Status    *db.Status
}

func (s *OrderBiz) UpdateOrder(ctx context.Context, params UpdateOrderParams) error {
	txStorage, err := s.storage.Begin(ctx)
	if err != nil {
		return err
	}
	defer txStorage.Rollback(ctx)

	getOrderParams := db.GetOrderParams{
		ID:     params.ID,
		Status: ptr.ToPtr(db.StatusPending),
	}

	// User only see their own payments
	if params.Role == db.RoleUser {
		getOrderParams.UserID = &params.AccountID
	}

	// Order must be pending
	payment, err := txStorage.GetOrder(ctx, getOrderParams)
	if err != nil {
		return err
	}

	// If payment method is cash, address is required
	if (params.Method == nil && payment.Method == db.OrderOrderMethodCash || params.Method != nil && *params.Method == db.OrderOrderMethodCash) &&
		(params.Address == nil && payment.Address == "" || params.Address != nil && *params.Address == "") {
		return fmt.Errorf("address is required for payment method %s", *params.Method)
	}

	// If params.Status is not nil and not admin, check if account (staff, ...) has permission to update status
	if params.Status != nil && params.Role != db.RoleAdmin {
		if ok, err := s.accountSvc.HasPermission(ctx, account.HasPermissionParams{
			AccountID: params.AccountID,
			Permissions: []db.Permission{
				db.PermissionUpdateOrder,
			},
		}); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("account %d does not have permission to update payment status", params.AccountID)
		}
	}

	if err = txStorage.UpdateOrder(ctx, db.UpdateOrderParams{
		ID:      params.ID,
		Method:  params.Method,
		Address: params.Address,
		Status:  params.Status,
	}); err != nil {
		return err
	}

	if err = txStorage.Commit(ctx); err != nil {
		return err
	}

	return nil
}

type CancelOrderParams = struct {
	UserID  int64
	OrderID int64
}

func (s *OrderBiz) CancelOrder(ctx context.Context, params CancelOrderParams) error {
	txStorage, err := s.storage.Begin(ctx)
	if err != nil {
		return err
	}
	defer txStorage.Rollback(ctx)

	payment, err := txStorage.GetOrder(ctx, db.GetOrderParams{
		ID:     params.OrderID,
		UserID: &params.UserID,
	})
	if err != nil {
		return err
	}

	// No need to check ownership as we already check it in GetOrder
	// if payment.UserID != *params.UserID {
	// 	return fmt.Errorf("payment %d not belong to user %d", params.OrderID, params.UserID)
	// }

	if payment.Status != db.StatusPending {
		return fmt.Errorf("payment %d cannot be canceled", params.OrderID)
	}

	if err = txStorage.UpdateOrder(ctx, db.UpdateOrderParams{
		ID:     params.OrderID,
		Status: ptr.ToPtr(db.StatusCanceled),
	}); err != nil {
		return err
	}

	if err = txStorage.Commit(ctx); err != nil {
		return err
	}

	return nil
}

type CancelRefundParams = struct {
	UserID   int64
	RefundID int64
}

func (s *OrderBiz) CancelRefund(ctx context.Context, params CancelRefundParams) error {
	txStorage, err := s.storage.BeginTx(ctx)
	if err != nil {
		return err
	}
	defer txStorage.Rollback(ctx)

	refund, err := txStorage.GetRefund(ctx, db.GetRefundParams{
		ID:     params.RefundID,
		UserID: &params.UserID,
	})
	if err != nil {
		return err
	}

	if refund.Status != db.StatusPending {
		return fmt.Errorf("refund %d cannot be canceled", params.RefundID)
	}

	if err = txStorage.UpdateRefund(ctx, db.UpdateRefundParams{
		ID:     params.RefundID,
		UserID: &params.UserID,
		Status: ptr.ToPtr(db.StatusCanceled),
	}); err != nil {
		return err
	}

	if err = txStorage.Commit(ctx); err != nil {
		return err
	}

	return nil
}
