package product

import (
	"context"

	"shopnexus-remastered/internal/model"
	"shopnexus-remastered/internal/service/storage"
)

func (s *ServiceImpl) GetBrand(ctx context.Context, id int64) (model.Brand, error) {
	brand, err := s.storage.GetBrand(ctx, id)
	if err != nil {
		return model.Brand{}, err
	}

	return brand, nil
}

type ListBrandsParams = storage.ListBrandsParams

func (s *ServiceImpl) ListBrands(ctx context.Context, params ListBrandsParams) (result model.PaginateResult[model.Brand], err error) {
	total, err := s.storage.CountBrands(ctx, params)
	if err != nil {
		return result, err
	}

	brands, err := s.storage.ListBrands(ctx, params)
	if err != nil {
		return result, err
	}

	return model.PaginateResult[model.Brand]{
		Data:       brands,
		Limit:      params.Limit,
		Page:       params.Page,
		Total:      total,
		NextPage:   params.NextPage(total),
		NextCursor: nil,
	}, nil
}

type CreateBrandParams struct {
	UserID int64
	model.Brand
}

func (s *ServiceImpl) CreateBrand(ctx context.Context, params CreateBrandParams) (model.Brand, error) {
	txStorage, err := s.storage.Begin(ctx)
	if err != nil {
		return model.Brand{}, err
	}
	defer txStorage.Rollback(ctx)

	newBrand, err := txStorage.CreateBrand(ctx, params.Brand)
	if err != nil {
		return model.Brand{}, err
	}

	if err = txStorage.Commit(ctx); err != nil {
		return model.Brand{}, err
	}

	return newBrand, nil
}

type UpdateBrandParams struct {
	StorageParams storage.UpdateBrandParams
	Resources     []string
}

func (s *ServiceImpl) UpdateBrand(ctx context.Context, params UpdateBrandParams) error {
	txStorage, err := s.storage.Begin(ctx)
	if err != nil {
		return err
	}
	defer txStorage.Rollback(ctx)

	if err = txStorage.UpdateBrand(ctx, params.StorageParams); err != nil {
		return err
	}

	if err = txStorage.UpdateResources(ctx, params.StorageParams.ID, model.ResourceTypeBrand, params.Resources); err != nil {
		return err
	}

	return txStorage.Commit(ctx)
}

func (s *ServiceImpl) DeleteBrand(ctx context.Context, id int64) error {
	return s.storage.DeleteBrand(ctx, id)
}
