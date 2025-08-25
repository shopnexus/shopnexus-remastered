package product

import (
	"context"

	"shopnexus-remastered/internal/model"
	"shopnexus-remastered/internal/service/storage"
)

type ListSalesParams struct {
	model.PaginationParams
	Tag             *string
	ProductModelID  *int64
	BrandID         *int64
	DateStartedFrom *int64
	DateStartedTo   *int64
	DateEndedFrom   *int64
	DateEndedTo     *int64
	IsActive        *bool
}

func (s *ServiceImpl) GetSale(ctx context.Context, id int64) (model.Sale, error) {
	return s.storage.GetSale(ctx, id)
}

func (s *ServiceImpl) ListSales(ctx context.Context, params ListSalesParams) (model.PaginateResult[model.Sale], error) {
	count, err := s.storage.CountSales(ctx, storage.ListSalesParams{
		PaginationParams: params.PaginationParams,
		Tag:              params.Tag,
		ProductModelID:   params.ProductModelID,
		BrandID:          params.BrandID,
		DateStartedFrom:  params.DateStartedFrom,
		DateStartedTo:    params.DateStartedTo,
		DateEndedFrom:    params.DateEndedFrom,
		DateEndedTo:      params.DateEndedTo,
		IsActive:         params.IsActive,
	})
	if err != nil {
		return model.PaginateResult[model.Sale]{}, err
	}

	data, err := s.storage.ListSales(ctx, storage.ListSalesParams{
		PaginationParams: params.PaginationParams,
		Tag:              params.Tag,
		ProductModelID:   params.ProductModelID,
		BrandID:          params.BrandID,
		DateStartedFrom:  params.DateStartedFrom,
		DateStartedTo:    params.DateStartedTo,
		DateEndedFrom:    params.DateEndedFrom,
		DateEndedTo:      params.DateEndedTo,
		IsActive:         params.IsActive,
	})
	if err != nil {
		return model.PaginateResult[model.Sale]{}, err
	}

	return model.PaginateResult[model.Sale]{
		Data:     data,
		Total:    count,
		Page:     params.Page,
		Limit:    params.Limit,
		NextPage: params.NextPage(count),
	}, nil
}

type CreateSaleParams struct {
	UserID int64
	Sale   model.Sale
}

func (s *ServiceImpl) CreateSale(ctx context.Context, params CreateSaleParams) (model.Sale, error) {
	return s.storage.CreateSale(ctx, params.Sale)
}

type UpdateSaleParams struct {
	ID              int64
	Tag             *string
	ProductModelID  *int64
	BrandID         *int64
	DateStarted     *int64
	DateEnded       *int64
	Quantity        *int64
	Used            *int64
	IsActive        *bool
	DiscountPercent *int32
	DiscountPrice   *int64
}

func (s *ServiceImpl) UpdateSale(ctx context.Context, params UpdateSaleParams) error {
	return s.storage.UpdateSale(ctx, storage.UpdateSaleParams{
		ID:              params.ID,
		Tag:             params.Tag,
		ProductModelID:  params.ProductModelID,
		BrandID:         params.BrandID,
		DateStarted:     params.DateStarted,
		DateEnded:       params.DateEnded,
		Quantity:        params.Quantity,
		Used:            params.Used,
		IsActive:        params.IsActive,
		DiscountPercent: params.DiscountPercent,
		DiscountPrice:   params.DiscountPrice,
	})
}

func (s *ServiceImpl) DeleteSale(ctx context.Context, id int64) error {
	return s.storage.DeleteSale(ctx, id)
}

func (s *ServiceImpl) GetAppliedSales(ctx context.Context, productID int64) ([]model.Sale, error) {
	product, err := s.storage.GetProduct(ctx, productID)
	if err != nil {
		return nil, err
	}

	productModel, err := s.storage.GetProductModel(ctx, product.ProductModelID)
	if err != nil {
		return nil, err
	}

	// Get available sales using the same approach as the payment system
	sales, err := s.storage.GetAvailableSales(ctx, storage.GetLatestSaleParams{
		ProductModelID: productModel.ID,
		BrandID:        productModel.BrandID,
		Tags:           productModel.Tags,
	})
	if err != nil {
		return nil, err
	}

	// for i := range sales {
	// 	if sales[i].DiscountPercent == nil && sales[i].DiscountPrice == nil {
	// 		// If both discount percent and discount price are nil, we can skip this sale
	// 		sales = slices.Delete(sales, i, i+1)
	// 		i-- // Adjust index after removal
	// 		continue
	// 	}
	// }

	for i := len(sales) - 1; i >= 0; i-- {
		if sales[i].DiscountPercent == nil && sales[i].DiscountPrice == nil {
			// If both discount percent and discount price are nil, we can skip this sale
			sales = slices.Delete(sales, i, i+1)
			continue
		}
	}

	return sales, nil
}
