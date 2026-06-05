package orderbiz

import (
	"fmt"
	"time"

	restate "github.com/restatedev/sdk-go"

	"github.com/google/uuid"

	orderdb "shopnexus-server/internal/module/order/db/sqlc"
)

// --- Param/Result structs ---

type GetSellerOrderStatsParams struct {
	SellerID  uuid.UUID `json:"seller_id"  validate:"required"`
	StartDate time.Time `json:"start_date" validate:"required"`
	EndDate   time.Time `json:"end_date"   validate:"required"`
}

type SellerOrderStats struct {
	TotalRevenue int64 `json:"total_revenue"`
	TotalOrders  int64 `json:"total_orders"`
	ItemsSold    int64 `json:"items_sold"`
}

type GetSellerOrderTimeSeriesParams struct {
	SellerID    uuid.UUID `json:"seller_id"   validate:"required"`
	StartDate   time.Time `json:"start_date"  validate:"required"`
	EndDate     time.Time `json:"end_date"    validate:"required"`
	Granularity string    `json:"granularity" validate:"required,oneof=day week month"`
}

type SellerOrderTimeSeriesPoint struct {
	Date       time.Time `json:"date"`
	Revenue    int64     `json:"revenue"`
	OrderCount int64     `json:"order_count"`
}

type GetSellerPendingActionsParams struct {
	SellerID uuid.UUID `json:"seller_id" validate:"required"`
}

type SellerPendingActions struct {
	PendingItems   int64 `json:"pending_items"`
	PendingRefunds int64 `json:"pending_refunds"`
}

type GetSellerTopProductsParams struct {
	SellerID  uuid.UUID `json:"seller_id"  validate:"required"`
	StartDate time.Time `json:"start_date" validate:"required"`
	EndDate   time.Time `json:"end_date"   validate:"required"`
	Limit     int32     `json:"limit"`
}

type SellerTopProduct struct {
	SkuID     uuid.UUID `json:"sku_id"`
	SkuName   string    `json:"sku_name"`
	SoldCount int64     `json:"sold_count"`
	Revenue   int64     `json:"revenue"`
}

// --- Implementations ---

func (b *dashboardHandler) GetSellerOrderStats(
	ctx restate.Context,
	params GetSellerOrderStatsParams,
) (SellerOrderStats, error) {
	row, err := b.storage.Querier().GetSellerOrderStats(ctx, orderdb.GetSellerOrderStatsParams{
		SellerID: params.SellerID,
		StartAt:  params.StartDate,
		EndAt:    params.EndDate,
	})
	if err != nil {
		return SellerOrderStats{}, fmt.Errorf("get seller order stats: %w", err)
	}
	return SellerOrderStats{
		TotalRevenue: row.TotalRevenue,
		TotalOrders:  row.TotalOrders,
		ItemsSold:    row.ItemsSold,
	}, nil
}

func (b *dashboardHandler) GetSellerOrderTimeSeries(
	ctx restate.Context,
	params GetSellerOrderTimeSeriesParams,
) ([]SellerOrderTimeSeriesPoint, error) {
	rows, err := b.storage.Querier().GetSellerOrderTimeSeries(ctx, orderdb.GetSellerOrderTimeSeriesParams{
		Granularity: params.Granularity,
		SellerID:    params.SellerID,
		StartAt:     params.StartDate,
		EndAt:       params.EndDate,
	})
	if err != nil {
		return nil, fmt.Errorf("get seller order time series: %w", err)
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

func (b *dashboardHandler) GetSellerPendingActions(
	ctx restate.Context,
	params GetSellerPendingActionsParams,
) (SellerPendingActions, error) {
	row, err := b.storage.Querier().GetSellerPendingActions(ctx, params.SellerID)
	if err != nil {
		return SellerPendingActions{}, fmt.Errorf("get seller pending actions: %w", err)
	}
	return SellerPendingActions{
		PendingItems:   row.PendingItems,
		PendingRefunds: row.PendingRefunds,
	}, nil
}

func (b *dashboardHandler) GetSellerTopProducts(
	ctx restate.Context,
	params GetSellerTopProductsParams,
) ([]SellerTopProduct, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 5
	}
	rows, err := b.storage.Querier().GetSellerTopProducts(ctx, orderdb.GetSellerTopProductsParams{
		SellerID: params.SellerID,
		StartAt:  params.StartDate,
		EndAt:    params.EndDate,
		TopLimit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("get seller top products: %w", err)
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
