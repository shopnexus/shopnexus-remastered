package catalog

import (
	"context"

	"shopnexus-remastered/internal/model"
	"shopnexus-remastered/internal/service/storage"
)

func (s *ServiceImpl) GetProductModel(ctx context.Context, id int64) (model.ProductModel, error) {
	productModel, err := s.storage.GetProductModel(ctx, id)
	if err != nil {
		return model.ProductModel{}, err
	}

	return productModel, nil
}

func (s *ServiceImpl) GetProductSerialIDs(ctx context.Context, productID int64) ([]string, error) {
	return s.storage.GetProductSerialIDs(ctx, productID)
}

type ListProductModelsParams = storage.ListProductModelsParams

func (s *ServiceImpl) ListProductModels(ctx context.Context, params ListProductModelsParams) (result model.PaginateResult[model.ProductModel], err error) {
	total, err := s.storage.CountProductModels(ctx, params)
	if err != nil {
		return result, err
	}

	productModels, err := s.storage.ListProductModels(ctx, params)
	if err != nil {
		return result, err
	}

	return model.PaginateResult[model.ProductModel]{
		Data:       productModels,
		Limit:      params.Limit,
		Page:       params.Page,
		Total:      total,
		NextPage:   params.NextPage(total),
		NextCursor: nil,
	}, nil
}

type CreateProductModelParams struct {
	UserID int64
	model.ProductModel
}

func (s *ServiceImpl) CreateProductModel(ctx context.Context, params CreateProductModelParams) (model.ProductModel, error) {
	txStorage, err := s.storage.Begin(ctx)
	if err != nil {
		return model.ProductModel{}, err
	}
	defer txStorage.Rollback(ctx)

	productModel, err := txStorage.CreateProductModel(ctx, params.ProductModel)
	if err != nil {
		return model.ProductModel{}, err
	}

	if err := txStorage.Commit(ctx); err != nil {
		return model.ProductModel{}, err
	}

	return productModel, nil
}

type UpdateProductModelParams = struct {
	ID               int64
	Type             *int64
	BrandID          *int64
	Name             *string
	Description      *string
	ListPrice        *int64
	DateManufactured *int64
	Resources        *[]string
	Tags             *[]string
}

func (s *ServiceImpl) UpdateProductModel(ctx context.Context, params UpdateProductModelParams) error {
	txStorage, err := s.storage.Begin(ctx)
	if err != nil {
		return err
	}
	defer txStorage.Rollback(ctx)

	if err = txStorage.UpdateProductModel(ctx, params); err != nil {
		return err
	}

	return txStorage.Commit(ctx)
}

func (s *ServiceImpl) DeleteProductModel(ctx context.Context, id int64) error {
	return s.storage.DeleteProductModel(ctx, id)
}

type ListProductTypesParams = storage.ListProductTypesParams

func (s *ServiceImpl) ListProductTypes(ctx context.Context, params ListProductTypesParams) ([]model.ProductType, error) {
	return s.storage.ListProductTypes(ctx, params)
}
