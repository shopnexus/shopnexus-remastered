package dashboard

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	catalogbiz "shopnexus-server/internal/module/catalog/biz"
)

type GetSellerDashboardParams struct {
	SellerID    uuid.UUID `json:"seller_id"   validate:"required"`
	StartDate   time.Time `json:"start_date"`
	EndDate     time.Time `json:"end_date"`
	Granularity string    `json:"granularity" validate:"omitempty,oneof=day week month"`
}

type SellerDashboard struct {
	Summary     DashboardSummary      `json:"summary"`
	Charts      DashboardCharts       `json:"charts"`
	TopProducts []DashboardTopProduct `json:"top_products"`
}

type DashboardSummary struct {
	TotalRevenue   int64               `json:"total_revenue"`
	TotalOrders    int64               `json:"total_orders"`
	ItemsSold      int64               `json:"items_sold"`
	AverageRating  float64             `json:"average_rating"`
	PendingItems   int64               `json:"pending_items"`
	PendingRefunds int64               `json:"pending_refunds"`
	Comparison     DashboardComparison `json:"comparison"`
}

type DashboardComparison struct {
	RevenueChange   *float64 `json:"revenue_change"`
	OrdersChange    *float64 `json:"orders_change"`
	ItemsSoldChange *float64 `json:"items_sold_change"`
}

type DashboardCharts struct {
	Revenue []TimeSeriesPoint `json:"revenue"`
	Orders  []TimeSeriesPoint `json:"orders"`
}

type TimeSeriesPoint struct {
	Date  time.Time `json:"date"`
	Value int64     `json:"value"`
}

type DashboardTopProduct struct {
	SkuID     uuid.UUID `json:"sku_id"`
	SkuName   string    `json:"sku_name"`
	SoldCount int64     `json:"sold_count"`
	Revenue   int64     `json:"revenue"`
}

func percentChange(current, previous int64) *float64 {
	if previous == 0 {
		return nil
	}
	change := float64(current-previous) / float64(previous) * 100
	return &change
}

// GetSellerDashboard composes the seller dashboard: order stats, time series,
// pending actions, and top products are read locally; vendor rating comes
// from the catalog module.
func (b *DashboardHandler) GetSellerDashboard(
	ctx context.Context,
	params GetSellerDashboardParams,
) (SellerDashboard, error) {
	var zero SellerDashboard

	// Defaults
	now := time.Now()
	if params.EndDate.IsZero() {
		params.EndDate = now
	}
	if params.StartDate.IsZero() {
		params.StartDate = params.EndDate.AddDate(0, 0, -30)
	}
	if params.Granularity == "" {
		params.Granularity = "day"
	}

	// Compute previous period
	duration := params.EndDate.Sub(params.StartDate)
	prevStart := params.StartDate.Add(-duration)
	prevEnd := params.StartDate

	currentStats, err := b.GetSellerOrderStats(ctx, GetSellerOrderStatsParams{
		SellerID:  params.SellerID,
		StartDate: params.StartDate,
		EndDate:   params.EndDate,
	})
	if err != nil {
		return zero, fmt.Errorf("get current order stats: %w", err)
	}

	prevStats, err := b.GetSellerOrderStats(ctx, GetSellerOrderStatsParams{
		SellerID:  params.SellerID,
		StartDate: prevStart,
		EndDate:   prevEnd,
	})
	if err != nil {
		return zero, fmt.Errorf("get previous order stats: %w", err)
	}

	timeSeries, err := b.GetSellerOrderTimeSeries(ctx, GetSellerOrderTimeSeriesParams{
		SellerID:    params.SellerID,
		StartDate:   params.StartDate,
		EndDate:     params.EndDate,
		Granularity: params.Granularity,
	})
	if err != nil {
		return zero, fmt.Errorf("get order time series: %w", err)
	}

	pending, err := b.GetSellerPendingActions(ctx, GetSellerPendingActionsParams{
		SellerID: params.SellerID,
	})
	if err != nil {
		return zero, fmt.Errorf("get pending actions: %w", err)
	}

	topProducts, err := b.GetSellerTopProducts(ctx, GetSellerTopProductsParams{
		SellerID:  params.SellerID,
		StartDate: params.StartDate,
		EndDate:   params.EndDate,
		Limit:     5,
	})
	if err != nil {
		return zero, fmt.Errorf("get top products: %w", err)
	}

	// Average rating from catalog
	vendorStats, err := b.catalog.GetVendorStats(ctx, catalogbiz.GetVendorStatsParams{
		AccountID: params.SellerID,
	})
	if err != nil {
		return zero, fmt.Errorf("get vendor stats: %w", err)
	}

	// Build charts
	revenuePoints := make([]TimeSeriesPoint, len(timeSeries))
	orderPoints := make([]TimeSeriesPoint, len(timeSeries))
	for i, ts := range timeSeries {
		revenuePoints[i] = TimeSeriesPoint{Date: ts.Date, Value: ts.Revenue}
		orderPoints[i] = TimeSeriesPoint{Date: ts.Date, Value: ts.OrderCount}
	}

	dashTopProducts := make([]DashboardTopProduct, len(topProducts))
	for i, tp := range topProducts {
		dashTopProducts[i] = DashboardTopProduct{
			SkuID:     tp.SkuID,
			SkuName:   tp.SkuName,
			SoldCount: tp.SoldCount,
			Revenue:   tp.Revenue,
		}
	}

	return SellerDashboard{
		Summary: DashboardSummary{
			TotalRevenue:   currentStats.TotalRevenue,
			TotalOrders:    currentStats.TotalOrders,
			ItemsSold:      currentStats.ItemsSold,
			AverageRating:  vendorStats.AverageRating,
			PendingItems:   pending.PendingItems,
			PendingRefunds: pending.PendingRefunds,
			Comparison: DashboardComparison{
				RevenueChange:   percentChange(currentStats.TotalRevenue, prevStats.TotalRevenue),
				OrdersChange:    percentChange(currentStats.TotalOrders, prevStats.TotalOrders),
				ItemsSoldChange: percentChange(currentStats.ItemsSold, prevStats.ItemsSold),
			},
		},
		Charts: DashboardCharts{
			Revenue: revenuePoints,
			Orders:  orderPoints,
		},
		TopProducts: dashTopProducts,
	}, nil
}
