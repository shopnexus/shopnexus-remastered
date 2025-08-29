package catalog

import (
	"context"

	"shopnexus-remastered/internal/model"
	"shopnexus-remastered/internal/service/account"
	"shopnexus-remastered/internal/service/storage"
)

func (s *ServiceImpl) GetProductSerial(ctx context.Context, serialID string) (model.ProductSerial, error) {
	return s.storage.GetProductSerial(ctx, serialID)
}

type ListProductSerialsParams = storage.ListProductSerialsParams

func (s *ServiceImpl) ListProductSerials(ctx context.Context, params ListProductSerialsParams) (result model.PaginateResult[model.ProductSerial], err error) {
	total, err := s.storage.CountProductSerials(ctx, params)
	if err != nil {
		return result, err
	}

	serials, err := s.storage.ListProductSerials(ctx, params)
	if err != nil {
		return result, err
	}

	return model.PaginateResult[model.ProductSerial]{
		Data:       serials,
		Limit:      params.Limit,
		Page:       params.Page,
		Total:      total,
		NextPage:   params.NextPage(total),
		NextCursor: nil,
	}, nil
}

func (s *ServiceImpl) CreateProductSerial(ctx context.Context, serial model.ProductSerial) (model.ProductSerial, error) {
	return s.storage.CreateProductSerial(ctx, serial)
}

type UpdateProductSerialParams = storage.UpdateProductSerialParams

func (s *ServiceImpl) UpdateProductSerial(ctx context.Context, params UpdateProductSerialParams) error {
	return s.storage.UpdateProductSerial(ctx, params)
}

type DeleteProductSerialPParams struct {
	AccountID int64
	Role      model.AccountType
	SerialID  string
}

func (s *ServiceImpl) DeleteProductSerial(ctx context.Context, params DeleteProductSerialPParams) error {
	hasPermission, err := s.accountSvc.HasPermission(ctx, account.HasPermissionParams{
		AccountID:   params.AccountID,
		Role:        &params.Role,
		Permissions: []model.Permission{model.PermissionDeleteProductSerial},
	})
	if err != nil {
		return err
	}

	if !hasPermission {
		return model.ErrPermissionDenied
	}

	return s.storage.DeleteProductSerial(ctx, params.SerialID)
}

func (s *ServiceImpl) MarkProductSerialsAsSold(ctx context.Context, serialIDs []string) error {
	return s.storage.MarkProductSerialsAsSold(ctx, serialIDs)
}
