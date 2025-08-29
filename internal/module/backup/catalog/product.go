package catalog

import (
	"context"

	"shopnexus-remastered/internal/model"
	"shopnexus-remastered/internal/service/account"
	"shopnexus-remastered/internal/service/storage"
)

type ServiceImpl struct {
	storage    storage.Service
	accountSvc account.Service
}

type Service interface {
	WithTx(txStorage *storage.TxStorage) (Service, error)

	GetBrand(ctx context.Context, id int64) (model.Brand, error)
	ListBrands(ctx context.Context, params ListBrandsParams) (model.PaginateResult[model.Brand], error)
	CreateBrand(ctx context.Context, params CreateBrandParams) (model.Brand, error)
	UpdateBrand(ctx context.Context, params UpdateBrandParams) error
	DeleteBrand(ctx context.Context, id int64) error

	GetProductModel(ctx context.Context, id int64) (model.ProductModel, error)
	ListProductModels(ctx context.Context, params ListProductModelsParams) (model.PaginateResult[model.ProductModel], error)
	CreateProductModel(ctx context.Context, params CreateProductModelParams) (model.ProductModel, error)
	UpdateProductModel(ctx context.Context, params UpdateProductModelParams) error
	DeleteProductModel(ctx context.Context, id int64) error
	ListProductTypes(ctx context.Context, params ListProductTypesParams) ([]model.ProductType, error)

	GetProduct(ctx context.Context, id int64) (model.Product, error)
	ListProducts(ctx context.Context, params ListProductsParams) (model.PaginateResult[model.Product], error)
	CreateProduct(ctx context.Context, product model.Product) (model.Product, error)
	UpdateProduct(ctx context.Context, params UpdateProductParams) error
	UpdateProductSold(ctx context.Context, params UpdateProductSoldParams) error
	DeleteProduct(ctx context.Context, id int64) error
	GetProductByPOPID(ctx context.Context, productOnPaymentID int64) (model.Product, error)

	GetProductSerial(ctx context.Context, serialID string) (model.ProductSerial, error)
	ListProductSerials(ctx context.Context, params ListProductSerialsParams) (model.PaginateResult[model.ProductSerial], error)
	CreateProductSerial(ctx context.Context, serial model.ProductSerial) (model.ProductSerial, error)
	UpdateProductSerial(ctx context.Context, params UpdateProductSerialParams) error
	DeleteProductSerial(ctx context.Context, params DeleteProductSerialPParams) error
	MarkProductSerialsAsSold(ctx context.Context, serialIDs []string) error

	GetProductSerialIDs(ctx context.Context, productID int64) ([]string, error)

	GetSale(ctx context.Context, id int64) (model.Sale, error)
	ListSales(ctx context.Context, params ListSalesParams) (model.PaginateResult[model.Sale], error)
	CreateSale(ctx context.Context, params CreateSaleParams) (model.Sale, error)
	UpdateSale(ctx context.Context, params UpdateSaleParams) error
	DeleteSale(ctx context.Context, id int64) error
	GetAppliedSales(ctx context.Context, productID int64) ([]model.Sale, error)

	GetTag(ctx context.Context, tag string) (TagResponse, error)
	ListTags(ctx context.Context, params ListTagsParams) (model.PaginateResult[TagResponse], error)
	CreateTag(ctx context.Context, tag model.Tag) error
	UpdateTag(ctx context.Context, params UpdateTagParams) error
	DeleteTag(ctx context.Context, tag string) error

	GetComment(ctx context.Context, id int64) (model.Comment, error)
	ListComments(ctx context.Context, params ListCommentsParams) (model.PaginateResult[model.Comment], error)
	CreateComment(ctx context.Context, comment CreateCommentParams) (model.Comment, error)
	UpdateComment(ctx context.Context, params UpdateCommentParams) error
	DeleteComment(ctx context.Context, params DeleteCommentParams) error
}

func NewService(storage storage.Service, accountSvc account.Service) (Service, error) {
	return &ServiceImpl{
		storage:    storage,
		accountSvc: accountSvc,
	}, nil
}

func (s *ServiceImpl) WithTx(txStorage *storage.TxStorage) (Service, error) {
	//TODO: Use WithTX to all injected service
	txAccountSvc, err := s.accountSvc.WithTx(txStorage)
	if err != nil {
		return nil, err
	}
	return NewService(txStorage, txAccountSvc)
}

func (s *ServiceImpl) GetProduct(ctx context.Context, id int64) (model.Product, error) {
	return s.storage.GetProduct(ctx, id)
}

type ListProductsParams = storage.ListProductsParams

func (s *ServiceImpl) ListProducts(ctx context.Context, params ListProductsParams) (result model.PaginateResult[model.Product], err error) {
	total, err := s.storage.CountProducts(ctx, params)
	if err != nil {
		return result, err
	}

	products, err := s.storage.ListProducts(ctx, params)
	if err != nil {
		return result, err
	}

	return model.PaginateResult[model.Product]{
		Data:       products,
		Limit:      params.Limit,
		Page:       params.Page,
		Total:      total,
		NextPage:   params.NextPage(total),
		NextCursor: nil,
	}, nil
}

func (s *ServiceImpl) CreateProduct(ctx context.Context, product model.Product) (model.Product, error) {
	newProduct, err := s.storage.CreateProduct(ctx, product)
	if err != nil {
		return model.Product{}, err
	}

	return newProduct, nil
}

type UpdateProductParams = struct {
	// TODO: sửa lại ko xài StorageParams, phải tự ghi ra hết
	ID             int64
	ProductModelID *int64
	Quantity       *int64
	Sold           *int64
	AddPrice       *int64
	CanCombine     *bool
	IsActive       *bool
	Metadata       *[]byte
	Resources      *[]string
}

func (s *ServiceImpl) UpdateProduct(ctx context.Context, params UpdateProductParams) error {
	txStorage, err := s.storage.Begin(ctx)
	if err != nil {
		return err
	}
	defer txStorage.Rollback(ctx)

	if err = s.storage.UpdateProduct(ctx, storage.UpdateProductParams{
		ID:              params.ID,
		ProductModelID:  params.ProductModelID,
		Quantity:        params.Quantity,
		Sold:            params.Sold,
		AdditionalPrice: params.AddPrice,
		CanCombine:      params.CanCombine,
		IsActive:        params.IsActive,
		Metadata:        params.Metadata,
		Resources:       params.Resources,
	}); err != nil {
		return err
	}

	if err = txStorage.Commit(ctx); err != nil {
		return err
	}

	return nil
}

type UpdateProductSoldParams = struct {
	IDs    []int64
	Amount int64
}

func (s *ServiceImpl) UpdateProductSold(ctx context.Context, params UpdateProductSoldParams) error {
	txStorage, err := s.storage.Begin(ctx)
	if err != nil {
		return err
	}
	defer txStorage.Rollback(ctx)

	if err = s.storage.UpdateProductSold(ctx, params.IDs, params.Amount); err != nil {
		return err
	}

	if err = txStorage.Commit(ctx); err != nil {
		return err
	}

	return nil
}

func (s *ServiceImpl) DeleteProduct(ctx context.Context, id int64) error {
	return s.storage.DeleteProduct(ctx, id)
}

func (s *ServiceImpl) GetProductByPOPID(ctx context.Context, productOnPaymentID int64) (model.Product, error) {
	product, err := s.storage.GetProductByPOPID(ctx, productOnPaymentID)
	if err != nil {
		return model.Product{}, err
	}
	return product, nil
}
