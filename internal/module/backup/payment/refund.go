package payment

import (
	"context"
	"fmt"

	"shopnexus-go-service/internal/model"
	"shopnexus-go-service/internal/service/account"
	"shopnexus-go-service/internal/service/storage"
)

type GetRefundParams struct {
	UserID   int64
	RefundID int64
}

func (s *ServiceImpl) GetRefund(ctx context.Context, params GetRefundParams) (model.Refund, error) {
	refund, err := s.storage.GetRefund(ctx, storage.GetRefundParams{
		ID:     params.RefundID,
		UserID: &params.UserID,
	})
	if err != nil {
		return model.Refund{}, err
	}

	return refund, nil
}

type ListRefundsParams struct {
	model.PaginationParams
	AccountID          int64
	Role               model.AccountType
	ProductOnPaymentID *int64
	Method             *model.RefundMethod
	Status             *model.Status
	Reason             *string
	Address            *string
	DateCreatedFrom    *int64
	DateCreatedTo      *int64
}

func (s *ServiceImpl) ListRefunds(ctx context.Context, params ListRefundsParams) (result model.PaginateResult[model.Refund], err error) {
	storageParams := storage.ListRefundsParams{
		PaginationParams: model.PaginationParams{
			Page:  params.Page,
			Limit: params.Limit,
		},
		ProductOnPaymentID: params.ProductOnPaymentID,
		Method:             params.Method,
		Status:             params.Status,
		Reason:             params.Reason,
		Address:            params.Address,
		DateCreatedFrom:    params.DateCreatedFrom,
		DateCreatedTo:      params.DateCreatedTo,
	}

	// User only can see their own refunds
	if params.Role == model.RoleUser {
		storageParams.UserID = &params.AccountID
	}

	total, err := s.storage.CountRefunds(ctx, storageParams)
	if err != nil {
		return result, err
	}

	refunds, err := s.storage.ListRefunds(ctx, storageParams)
	if err != nil {
		return result, err
	}

	return model.PaginateResult[model.Refund]{
		Data:       refunds,
		Limit:      params.Limit,
		Page:       params.Page,
		Total:      total,
		NextPage:   params.NextPage(total),
		NextCursor: nil,
	}, nil
}

type CreateRefundParams struct {
	UserID             int64
	ProductOnPaymentID int64
	Method             model.RefundMethod
	Reason             string
	Address            string
	Resources          []string
}

func (s *ServiceImpl) CreateRefund(ctx context.Context, params CreateRefundParams) (model.Refund, error) {
	txStorage, err := s.storage.Begin(ctx)
	if err != nil {
		return model.Refund{}, err
	}
	defer txStorage.Rollback(ctx)

	// Method drop_off must not contains address
	if params.Method == model.RefundMethodDropOff && params.Address != "" {
		return model.Refund{}, fmt.Errorf("address is not required for refund method drop_off %w", model.ErrMalformedParams)
	}

	// Method pick_up must contains address
	if params.Method == model.RefundMethodPickUp && params.Address == "" {
		return model.Refund{}, fmt.Errorf("address is required for refund method pick_up %w", model.ErrMalformedParams)
	}

	// Check if refund is allowed
	canRefund, err := txStorage.CanRefund(ctx, storage.CanRefundParams{
		ProductOnPaymentID: params.ProductOnPaymentID,
		UserID:             &params.UserID,
	})
	if err != nil {
		return model.Refund{}, err
	}
	if !canRefund {
		return model.Refund{}, fmt.Errorf("refund for payment product %d is not allowed", params.ProductOnPaymentID)
	}

	refund, err := txStorage.CreateRefund(ctx, model.Refund{
		ProductOnPaymentID: params.ProductOnPaymentID,
		Method:             params.Method,
		Status:             model.StatusPending,
		Reason:             params.Reason,
		Address:            params.Address,
		Resources:          params.Resources,
	})
	if err != nil {
		return model.Refund{}, err
	}

	if err = txStorage.Commit(ctx); err != nil {
		return model.Refund{}, err
	}

	return refund, nil
}

type UpdateRefundParams struct {
	ID        int64
	Role      model.AccountType
	UserID    int64
	Method    *model.RefundMethod
	Status    *model.Status
	Reason    *string
	Address   *string
	Resources *[]string
}

func (s *ServiceImpl) UpdateRefund(ctx context.Context, params UpdateRefundParams) error {
	txStorage, err := s.storage.Begin(ctx)
	if err != nil {
		return err
	}
	defer txStorage.Rollback(ctx)

	storageParams := storage.UpdateRefundParams{
		ID:        params.ID,
		Method:    params.Method,
		Status:    params.Status,
		Reason:    params.Reason,
		Address:   params.Address,
		Resources: params.Resources,
	}

	// User only can update their own refunds
	if params.Role == model.RoleUser {
		storageParams.UserID = &params.UserID
		if params.Status != nil {
			return fmt.Errorf("user %d has no permission to update refund status: %w", params.UserID, model.ErrForbidden)
		}
	}

	if params.Status != nil {
		// Check if account has permission to update refund status
		if ok, err := s.accountSvc.HasPermission(ctx, account.HasPermissionParams{
			AccountID: params.UserID,
			Role:      &params.Role,
			Permissions: []model.Permission{
				model.PermissionUpdateRefund,
			},
		}); !ok {
			return fmt.Errorf("account %d has no permission to update refund status: %w", params.UserID, err)
		}
	}

	// TODO: wrong check because user can also update address, need pass UserID into
	refund, err := txStorage.GetRefund(ctx, storage.GetRefundParams{
		ID: params.ID,
	})
	if err != nil {
		return fmt.Errorf("failed to get refund %d: %w", params.ID, err)
	}

	// Method drop_off must not contain address
	if (params.Method != nil && *params.Method == model.RefundMethodDropOff) || refund.Method == model.RefundMethodDropOff {
		storageParams.Address = nil
	}

	// Method drop_off must not contain address
	if *params.Method == model.RefundMethodDropOff {
		storageParams.Address = nil
	}

	if err = txStorage.UpdateRefund(ctx, storageParams); err != nil {
		return err
	}

	return txStorage.Commit(ctx)
}
