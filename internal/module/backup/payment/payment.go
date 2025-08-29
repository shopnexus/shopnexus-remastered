package payment

import (
	"context"
	"fmt"

	"shopnexus-go-service/config"
	"shopnexus-go-service/internal/client/vnpay"
	"shopnexus-go-service/internal/model"
	"shopnexus-go-service/internal/service/account"
	"shopnexus-go-service/internal/service/product"
	"shopnexus-go-service/internal/service/storage"
	"shopnexus-go-service/internal/utils/ptr"
)

type ServiceImpl struct {
	storage    storage.Service
	accountSvc account.Service
	productSvc product.Service
	platforms  map[Platform]PaymentPlatform
}

func NewService(
	storage storage.Service,
	accountSvc account.Service,
	productSvc product.Service,
) (Service, error) {
	s := &ServiceImpl{
		storage:    storage,
		accountSvc: accountSvc,
		productSvc: productSvc,
		platforms:  map[Platform]PaymentPlatform{},
	}

	// Init payment platforms
	vnpayPlatform := &VnpayPlatform{
		client: vnpay.NewClient(vnpay.ClientOptions{
			TmnCode:    config.GetConfig().Vnpay.TmnCode,
			HashSecret: config.GetConfig().Vnpay.HashSecret,
		}),
	}
	s.platforms[PlatformVNPAY] = vnpayPlatform

	return s, nil
}

type Service interface {
	// Payment
	GetPayment(ctx context.Context, params GetPaymentParams) (model.Payment, error)
	ListPayments(ctx context.Context, params ListPaymentsParams) (model.PaginateResult[model.Payment], error)
	CreatePayment(ctx context.Context, params CreatePaymentParams) (CreatePaymentResult, error)
	UpdatePayment(ctx context.Context, params UpdatePaymentParams) error
	CancelPayment(ctx context.Context, params CancelPaymentParams) error

	// Refund
	GetRefund(ctx context.Context, params GetRefundParams) (model.Refund, error)
	ListRefunds(ctx context.Context, params ListRefundsParams) (model.PaginateResult[model.Refund], error)
	CreateRefund(ctx context.Context, params CreateRefundParams) (model.Refund, error)
	UpdateRefund(ctx context.Context, params UpdateRefundParams) error
	CancelRefund(ctx context.Context, params CancelRefundParams) error
}

func (s *ServiceImpl) WithTx(txStorage *storage.TxStorage) (Service, error) {
	return NewService(txStorage, s.accountSvc, s.productSvc)
}

type GetPaymentParams = struct {
	AccountID int64
	Role      model.AccountType
	PaymentID int64
}

func (s *ServiceImpl) GetPayment(ctx context.Context, params GetPaymentParams) (model.Payment, error) {
	storageParams := storage.GetPaymentParams{
		ID: params.PaymentID,
	}

	if params.Role == model.RoleUser {
		storageParams.UserID = &params.AccountID
	}

	return s.storage.GetPayment(ctx, storageParams)
}

type ListPaymentsParams struct {
	model.PaginationParams
	AccountID       int64
	Role            model.AccountType
	Method          *model.PaymentMethod
	Status          *model.Status
	Address         *string
	TotalFrom       *int64
	TotalTo         *int64
	DateCreatedFrom *int64
	DateCreatedTo   *int64
}

func (s *ServiceImpl) ListPayments(ctx context.Context, params ListPaymentsParams) (result model.PaginateResult[model.Payment], err error) {
	storageParams := storage.ListPaymentsParams{
		PaginationParams: params.PaginationParams,
		Method:           params.Method,
		Status:           params.Status,
		Address:          params.Address,
		TotalFrom:        params.TotalFrom,
		TotalTo:          params.TotalTo,
		DateCreatedFrom:  params.DateCreatedFrom,
		DateCreatedTo:    params.DateCreatedTo,
	}

	// User only see their own payments
	if params.Role == model.RoleUser {
		storageParams.UserID = &params.AccountID
	}

	total, err := s.storage.CountPayments(ctx, storageParams)
	if err != nil {
		return result, err
	}

	payments, err := s.storage.ListPayments(ctx, storageParams)
	if err != nil {
		return result, err
	}

	return model.PaginateResult[model.Payment]{
		Data:       payments,
		Limit:      params.Limit,
		Page:       params.Page,
		Total:      total,
		NextPage:   params.NextPage(total),
		NextCursor: nil,
	}, nil
}

type CreatePaymentParams struct {
	UserID        int64
	Address       string
	PaymentMethod model.PaymentMethod
	ProductIDs    []int64
}

type CreatePaymentResult struct {
	Payment model.Payment
	Url     string
}

func (s *ServiceImpl) CreatePayment(ctx context.Context, params CreatePaymentParams) (CreatePaymentResult, error) {
	txStorage, err := s.storage.Begin(ctx)
	if err != nil {
		return CreatePaymentResult{}, err
	}
	defer txStorage.Rollback(ctx)

	// Get user cart and filter by product ids
	cart, err := txStorage.GetCart(ctx, storage.GetCartParams{
		CartID:     params.UserID,
		ProductIDs: params.ProductIDs,
	})
	if err != nil {
		return CreatePaymentResult{}, err
	}

	if len(cart.Products) == 0 {
		return CreatePaymentResult{}, fmt.Errorf("cart is empty")
	}

	// Remove products from cart
	if err = txStorage.RemoveCartItem(ctx, cart.ID, params.ProductIDs); err != nil {
		return CreatePaymentResult{}, err
	}

	var (
		productOnPayments []model.ProductOnPayment
		totalPayment      int64
	)

	// Calculate total payment
	// Iterate through each product model in the cart
	for _, cartProduct := range cart.Products {
		// Get product details
		product, err := txStorage.GetProduct(ctx, cartProduct.GetID())
		if err != nil {
			return CreatePaymentResult{}, err
		}

		// Get product model details
		productModel, err := txStorage.GetProductModel(ctx, product.ProductModelID)
		if err != nil {
			return CreatePaymentResult{}, err
		}

		// Get any available product serial_ids from that product
		var serialIDs []string
		productSerials, err := txStorage.GetAvailableProducts(
			ctx,
			cartProduct.GetID(),
			cartProduct.GetQuantity(),
		)
		if err != nil {
			return CreatePaymentResult{}, err
		}
		for _, productSerial := range productSerials {
			serialIDs = append(serialIDs, productSerial.SerialID)
		}

		// Get available sales for the product model
		sales, err := txStorage.GetAvailableSales(ctx, storage.GetLatestSaleParams{
			ProductModelID: productModel.ID,
			BrandID:        productModel.BrandID,
			Tags:           productModel.Tags,
		})
		if err != nil {
			return CreatePaymentResult{}, err
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

		totalPayment += combinePrice - combineDiscount

		// If product can combine, add all quantity at once
		if product.CanCombine {
			productOnPayments = append(productOnPayments, model.ProductOnPayment{
				ItemQuantityBase: model.ItemQuantityBase[int64]{
					ItemID:   cartProduct.GetID(),
					Quantity: cartProduct.GetQuantity(),
				},
				SerialIDs:  serialIDs,
				Price:      combinePrice,
				TotalPrice: combinePrice - combineDiscount,
			})
		} else {
			for i := int64(0); i < cartProduct.GetQuantity(); i++ {
				productOnPayments = append(productOnPayments, model.ProductOnPayment{
					ItemQuantityBase: model.ItemQuantityBase[int64]{
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
	newPayment, err := txStorage.CreatePayment(ctx, model.Payment{
		UserID:   params.UserID,
		Address:  params.Address,
		Method:   params.PaymentMethod,
		Total:    totalPayment,
		Status:   model.StatusPending,
		Products: productOnPayments,
	})
	if err != nil {
		return CreatePaymentResult{}, err
	}

	// Create payment url
	var pp PaymentPlatform

	switch params.PaymentMethod {
	case model.PaymentMethodVnpay:
		pp, err = s.getPlatform(PlatformVNPAY)
		if err != nil {
			return CreatePaymentResult{}, err
		}
	case model.PaymentMethodMomo:
		// TODO: support momo payment
		return CreatePaymentResult{}, fmt.Errorf("payment method momo not yet supported")
		// pp, err = s.GetPlatform(PlatformMOMO)
		// if err != nil {
		// 	return CreatePaymentResult{}, err
		// }
	case model.PaymentMethodCash:
		// Do nothing
		// TODO: add logic for cash payment
		return CreatePaymentResult{}, fmt.Errorf("payment method cash not yet supported")
	default:
		return CreatePaymentResult{}, fmt.Errorf("payment method %s not supported", params.PaymentMethod)
	}

	url, err := pp.CreateOrder(ctx, CreateOrderParams{
		PaymentID: newPayment.ID,
		Info:      fmt.Sprintf("Payment for order %d", newPayment.ID),
		Amount:    newPayment.Total,
	})
	if err != nil {
		return CreatePaymentResult{}, err
	}

	// TODO: move this update product sold to cron job check success payment (because currently we don't know if payment is success or not)
	txProductSvc, err := s.productSvc.WithTx(txStorage)
	if err != nil {
		return CreatePaymentResult{}, err
	}
	if err = txProductSvc.UpdateProductSold(ctx, product.UpdateProductSoldParams{
		IDs: func() []int64 {
			ids := make([]int64, 0, len(productOnPayments))
			for _, pop := range productOnPayments {
				ids = append(ids, pop.ItemID)
			}
			return ids
		}(),
		Amount: 1,
	}); err != nil {
		return CreatePaymentResult{}, err
	}

	// Rollback if purchase failed
	if err = txStorage.Commit(ctx); err != nil {
		return CreatePaymentResult{}, err
	}

	return CreatePaymentResult{Payment: newPayment, Url: url}, nil
}

func (s *ServiceImpl) getPlatform(platform Platform) (PaymentPlatform, error) {
	pp, ok := s.platforms[platform]
	if !ok {
		return nil, fmt.Errorf("platform %s not found", platform)
	}
	return pp, nil
}

type UpdatePaymentParams struct {
	ID        int64
	AccountID int64
	Role      model.AccountType
	Method    *model.PaymentMethod
	Address   *string
	Status    *model.Status
}

func (s *ServiceImpl) UpdatePayment(ctx context.Context, params UpdatePaymentParams) error {
	txStorage, err := s.storage.Begin(ctx)
	if err != nil {
		return err
	}
	defer txStorage.Rollback(ctx)

	getPaymentParams := storage.GetPaymentParams{
		ID:     params.ID,
		Status: ptr.ToPtr(model.StatusPending),
	}

	// User only see their own payments
	if params.Role == model.RoleUser {
		getPaymentParams.UserID = &params.AccountID
	}

	// Payment must be pending
	payment, err := txStorage.GetPayment(ctx, getPaymentParams)
	if err != nil {
		return err
	}

	// If payment method is cash, address is required
	if (params.Method == nil && payment.Method == model.PaymentMethodCash || params.Method != nil && *params.Method == model.PaymentMethodCash) &&
		(params.Address == nil && payment.Address == "" || params.Address != nil && *params.Address == "") {
		return fmt.Errorf("address is required for payment method %s", *params.Method)
	}

	// If params.Status is not nil and not admin, check if account (staff, ...) has permission to update status
	if params.Status != nil && params.Role != model.RoleAdmin {
		if ok, err := s.accountSvc.HasPermission(ctx, account.HasPermissionParams{
			AccountID: params.AccountID,
			Permissions: []model.Permission{
				model.PermissionUpdatePayment,
			},
		}); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("account %d does not have permission to update payment status", params.AccountID)
		}
	}

	if err = txStorage.UpdatePayment(ctx, storage.UpdatePaymentParams{
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

type CancelPaymentParams = struct {
	UserID    int64
	PaymentID int64
}

func (s *ServiceImpl) CancelPayment(ctx context.Context, params CancelPaymentParams) error {
	txStorage, err := s.storage.Begin(ctx)
	if err != nil {
		return err
	}
	defer txStorage.Rollback(ctx)

	payment, err := txStorage.GetPayment(ctx, storage.GetPaymentParams{
		ID:     params.PaymentID,
		UserID: &params.UserID,
	})
	if err != nil {
		return err
	}

	// No need to check ownership as we already check it in GetPayment
	// if payment.UserID != *params.UserID {
	// 	return fmt.Errorf("payment %d not belong to user %d", params.PaymentID, params.UserID)
	// }

	if payment.Status != model.StatusPending {
		return fmt.Errorf("payment %d cannot be canceled", params.PaymentID)
	}

	if err = txStorage.UpdatePayment(ctx, storage.UpdatePaymentParams{
		ID:     params.PaymentID,
		Status: ptr.ToPtr(model.StatusCanceled),
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

func (s *ServiceImpl) CancelRefund(ctx context.Context, params CancelRefundParams) error {
	txStorage, err := s.storage.Begin(ctx)
	if err != nil {
		return err
	}
	defer txStorage.Rollback(ctx)

	refund, err := txStorage.GetRefund(ctx, storage.GetRefundParams{
		ID:     params.RefundID,
		UserID: &params.UserID,
	})
	if err != nil {
		return err
	}

	if refund.Status != model.StatusPending {
		return fmt.Errorf("refund %d cannot be canceled", params.RefundID)
	}

	if err = txStorage.UpdateRefund(ctx, storage.UpdateRefundParams{
		ID:     params.RefundID,
		UserID: &params.UserID,
		Status: ptr.ToPtr(model.StatusCanceled),
	}); err != nil {
		return err
	}

	if err = txStorage.Commit(ctx); err != nil {
		return err
	}

	return nil
}
