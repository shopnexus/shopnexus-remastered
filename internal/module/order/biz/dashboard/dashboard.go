package dashboard

import (
	"context"
	"fmt"
	"time"

	restate "github.com/restatedev/sdk-go"

	"github.com/google/uuid"

	catalogbiz "shopnexus-server/internal/module/catalog/biz"
	"shopnexus-server/internal/module/order/biz/base"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	"shopnexus-server/internal/shared/validator"
)

// DashboardHandler implements DashboardBiz over the shared core. It owns the
// composed seller dashboard: order stats are read locally, vendor stats come
// from the catalog module (forward dependency only).
type DashboardHandler struct {
	*base.Base

	catalog catalogbiz.CatalogBizClient
}

func New(c *base.Base, catalog catalogbiz.CatalogBizClient) *DashboardHandler {
	return &DashboardHandler{c, catalog}
}

// DashboardBiz covers seller dashboard aggregates.
type DashboardBiz interface {
	GetSellerOrderStats(ctx context.Context, params GetSellerOrderStatsParams) (SellerOrderStats, error)
	GetSellerOrderTimeSeries(
		ctx context.Context,
		params GetSellerOrderTimeSeriesParams,
	) ([]SellerOrderTimeSeriesPoint, error)
	GetSellerPendingActions(ctx context.Context, params GetSellerPendingActionsParams) (SellerPendingActions, error)
	GetSellerTopProducts(ctx context.Context, params GetSellerTopProductsParams) ([]SellerTopProduct, error)
	GetSellerDashboard(ctx context.Context, params GetSellerDashboardParams) (SellerDashboard, error)
}

type GetSellerOrderStatsParams struct {
	SellerID  uuid.UUID `validate:"required"`
	StartDate time.Time `validate:"required"`
	EndDate   time.Time `validate:"required"`
}

type SellerOrderStats struct {
	TotalRevenue int64 `json:"total_revenue"`
	TotalOrders  int64 `json:"total_orders"`
	ItemsSold    int64 `json:"items_sold"`
}

func (b *DashboardHandler) GetSellerOrderStats(
	ctx restate.Context,
	params GetSellerOrderStatsParams,
) (SellerOrderStats, error) {
	if err := validator.Validate(params); err != nil {
		return SellerOrderStats{}, fmt.Errorf("validate get seller order stats params: %w", err)
	}
	row, err := b.Storage.Querier().GetSellerOrderStats(ctx, orderdb.GetSellerOrderStatsParams{
		SellerID: params.SellerID,
		StartAt:  params.StartDate,
		EndAt:    params.EndDate,
	})
	if err != nil {
		return SellerOrderStats{}, fmt.Errorf("db get seller order stats: %w", err)
	}
	return SellerOrderStats{
		TotalRevenue: row.TotalRevenue,
		TotalOrders:  row.TotalOrders,
		ItemsSold:    row.ItemsSold,
	}, nil
}

type GetSellerOrderTimeSeriesParams struct {
	SellerID    uuid.UUID `validate:"required"`
	StartDate   time.Time `validate:"required"`
	EndDate     time.Time `validate:"required"`
	Granularity string    `validate:"required,oneof=day week month"`
}

type SellerOrderTimeSeriesPoint struct {
	Date       time.Time `json:"date"`
	Revenue    int64     `json:"revenue"`
	OrderCount int64     `json:"order_count"`
}

func (b *DashboardHandler) GetSellerOrderTimeSeries(
	ctx restate.Context,
	params GetSellerOrderTimeSeriesParams,
) ([]SellerOrderTimeSeriesPoint, error) {
	if err := validator.Validate(params); err != nil {
		return nil, fmt.Errorf("validate get seller order time series params: %w", err)
	}
	rows, err := b.Storage.Querier().GetSellerOrderTimeSeries(ctx, orderdb.GetSellerOrderTimeSeriesParams{
		Granularity: params.Granularity,
		SellerID:    params.SellerID,
		StartAt:     params.StartDate,
		EndAt:       params.EndDate,
	})
	if err != nil {
		return nil, fmt.Errorf("db get seller order time series: %w", err)
	}

	points := make([]SellerOrderTimeSeriesPoint, len(rows))
	for i, r := range rows {
		points[i] = SellerOrderTimeSeriesPoint{
			Date:       r.Bucket,
			Revenue:    r.Revenue,
			OrderCount: r.OrderCount,
		}
	}
	return points, nil
}

type GetSellerPendingActionsParams struct {
	SellerID uuid.UUID `validate:"required"`
}

type SellerPendingActions struct {
	PendingItems   int64 `json:"pending_items"`
	PendingRefunds int64 `json:"pending_refunds"`
}

func (b *DashboardHandler) GetSellerPendingActions(
	ctx restate.Context,
	params GetSellerPendingActionsParams,
) (SellerPendingActions, error) {
	if err := validator.Validate(params); err != nil {
		return SellerPendingActions{}, fmt.Errorf("validate get seller pending actions params: %w", err)
	}
	row, err := b.Storage.Querier().GetSellerPendingActions(ctx, params.SellerID)
	if err != nil {
		return SellerPendingActions{}, fmt.Errorf("db get seller pending actions: %w", err)
	}
	return SellerPendingActions{
		PendingItems:   row.PendingItems,
		PendingRefunds: row.PendingRefunds,
	}, nil
}

type GetSellerTopProductsParams struct {
	SellerID  uuid.UUID `validate:"required"`
	StartDate time.Time `validate:"required"`
	EndDate   time.Time `validate:"required"`
	Limit     int32
}

type SellerTopProduct struct {
	SkuID     uuid.UUID `json:"sku_id"`
	SkuName   string    `json:"sku_name"`
	SoldCount int64     `json:"sold_count"`
	Revenue   int64     `json:"revenue"`
}

func (b *DashboardHandler) GetSellerTopProducts(
	ctx restate.Context,
	params GetSellerTopProductsParams,
) ([]SellerTopProduct, error) {
	if err := validator.Validate(params); err != nil {
		return nil, fmt.Errorf("validate get seller top products params: %w", err)
	}
	limit := params.Limit
	if limit <= 0 {
		limit = 5
	}
	rows, err := b.Storage.Querier().GetSellerTopProducts(ctx, orderdb.GetSellerTopProductsParams{
		SellerID: params.SellerID,
		StartAt:  params.StartDate,
		EndAt:    params.EndDate,
		TopLimit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("db get seller top products: %w", err)
	}

	products := make([]SellerTopProduct, len(rows))
	for i, r := range rows {
		products[i] = SellerTopProduct{
			SkuID:     r.SkuID,
			SkuName:   r.SkuName,
			SoldCount: r.SoldCount,
			Revenue:   r.Revenue,
		}
	}
	return products, nil
}
