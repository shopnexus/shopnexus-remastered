package analyticbiz

import (
	"fmt"
	"time"

	restate "github.com/restatedev/sdk-go"

	"github.com/google/uuid"

	analyticmodel "shopnexus-server/internal/module/analytic/model"
)

// Local param/result types that mirror the Order and Catalog biz structs via JSON.
// These avoid circular imports: order/biz and catalog/biz both import analytic/biz.

type dashSellerOrderStatsParams struct {
	SellerID  uuid.UUID `json:"seller_id"`
	StartDate time.Time `json:"start_date"`
	EndDate   time.Time `json:"end_date"`
}

type dashSellerOrderStats struct {
	TotalRevenue int64 `json:"total_revenue"`
	TotalOrders  int64 `json:"total_orders"`
	ItemsSold    int64 `json:"items_sold"`
}

type dashSellerOrderTimeSeriesParams struct {
	SellerID    uuid.UUID `json:"seller_id"`
	StartDate   time.Time `json:"start_date"`
	EndDate     time.Time `json:"end_date"`
	Granularity string    `json:"granularity"`
}

type dashSellerOrderTimeSeriesPoint struct {
	Date       time.Time `json:"date"`
	Revenue    int64     `json:"revenue"`
	OrderCount int64     `json:"order_count"`
}

type dashSellerPendingActionsParams struct {
	SellerID uuid.UUID `json:"seller_id"`
}

type dashSellerPendingActions struct {
	PendingItems   int64 `json:"pending_items"`
	PendingRefunds int64 `json:"pending_refunds"`
}

type dashSellerTopProductsParams struct {
	SellerID  uuid.UUID `json:"seller_id"`
	StartDate time.Time `json:"start_date"`
	EndDate   time.Time `json:"end_date"`
	Limit     int32     `json:"limit"`
}

type dashSellerTopProduct struct {
	SkuID     uuid.UUID `json:"sku_id"`
	SkuName   string    `json:"sku_name"`
	SoldCount int64     `json:"sold_count"`
	Revenue   int64     `json:"revenue"`
}

type dashGetVendorStatsParams struct {
	AccountID uuid.UUID `json:"account_id"`
}

type dashVendorStats struct {
	ProductCount  int64   `json:"product_count"`
	AverageRating float64 `json:"average_rating"`
	TotalSold     int64   `json:"total_sold"`
	ResponseRate  float64 `json:"response_rate"`
}

func percentChange(current, previous int64) *float64 {
	if previous == 0 {
		return nil
	}
	change := float64(current-previous) / float64(previous) * 100
	return &change
}

func (b *AnalyticHandler) GetSellerDashboard(
	ctx restate.Context,
	params analyticmodel.GetSellerDashboardParams,
) (analyticmodel.SellerDashboard, error) {
	var zero analyticmodel.SellerDashboard

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

	// Current period stats
	currentStats, err := restate.Service[dashSellerOrderStats](ctx, "Order", "GetSellerOrderStats").
		Request(dashSellerOrderStatsParams{
			SellerID:  params.SellerID,
			StartDate: params.StartDate,
			EndDate:   params.EndDate,
		})
	if err != nil {
		return zero, fmt.Errorf("get current order stats: %w", err)
	}

	// Previous period stats (for comparison)
	prevStats, err := restate.Service[dashSellerOrderStats](ctx, "Order", "GetSellerOrderStats").
		Request(dashSellerOrderStatsParams{
			SellerID:  params.SellerID,
			StartDate: prevStart,
			EndDate:   prevEnd,
		})
	if err != nil {
		return zero, fmt.Errorf("get previous order stats: %w", err)
	}

	// Time series
	timeSeries, err := restate.Service[[]dashSellerOrderTimeSeriesPoint](ctx, "Order", "GetSellerOrderTimeSeries").
		Request(dashSellerOrderTimeSeriesParams{
			SellerID:    params.SellerID,
			StartDate:   params.StartDate,
			EndDate:     params.EndDate,
			Granularity: params.Granularity,
		})
	if err != nil {
		return zero, fmt.Errorf("get order time series: %w", err)
	}

	// Pending actions
	pending, err := restate.Service[dashSellerPendingActions](ctx, "Order", "GetSellerPendingActions").
		Request(dashSellerPendingActionsParams{
			SellerID: params.SellerID,
		})
	if err != nil {
		return zero, fmt.Errorf("get pending actions: %w", err)
	}

	// Top products
	topProducts, err := restate.Service[[]dashSellerTopProduct](ctx, "Order", "GetSellerTopProducts").
		Request(dashSellerTopProductsParams{
			SellerID:  params.SellerID,
			StartDate: params.StartDate,
			EndDate:   params.EndDate,
			Limit:     5,
		})
	if err != nil {
		return zero, fmt.Errorf("get top products: %w", err)
	}

	// Average rating from catalog
	vendorStats, err := restate.Service[dashVendorStats](ctx, "Catalog", "GetVendorStats").
		Request(dashGetVendorStatsParams{
			AccountID: params.SellerID,
		})
	if err != nil {
		return zero, fmt.Errorf("get vendor stats: %w", err)
	}

	// Build charts
	revenuePoints := make([]analyticmodel.TimeSeriesPoint, len(timeSeries))
	orderPoints := make([]analyticmodel.TimeSeriesPoint, len(timeSeries))
	for i, ts := range timeSeries {
		revenuePoints[i] = analyticmodel.TimeSeriesPoint{Date: ts.Date, Value: ts.Revenue}
		orderPoints[i] = analyticmodel.TimeSeriesPoint{Date: ts.Date, Value: ts.OrderCount}
	}

	// Build top products
	dashTopProducts := make([]analyticmodel.DashboardTopProduct, len(topProducts))
	for i, tp := range topProducts {
		dashTopProducts[i] = analyticmodel.DashboardTopProduct{
			SkuID:     tp.SkuID,
			SkuName:   tp.SkuName,
			SoldCount: tp.SoldCount,
			Revenue:   tp.Revenue,
		}
	}

	return analyticmodel.SellerDashboard{
		Summary: analyticmodel.DashboardSummary{
			TotalRevenue:   currentStats.TotalRevenue,
			TotalOrders:    currentStats.TotalOrders,
			ItemsSold:      currentStats.ItemsSold,
			AverageRating:  vendorStats.AverageRating,
			PendingItems:   pending.PendingItems,
			PendingRefunds: pending.PendingRefunds,
			Comparison: analyticmodel.DashboardComparison{
				RevenueChange:   percentChange(currentStats.TotalRevenue, prevStats.TotalRevenue),
				OrdersChange:    percentChange(currentStats.TotalOrders, prevStats.TotalOrders),
				ItemsSoldChange: percentChange(currentStats.ItemsSold, prevStats.ItemsSold),
			},
		},
		Charts: analyticmodel.DashboardCharts{
			Revenue: revenuePoints,
			Orders:  orderPoints,
		},
		TopProducts: dashTopProducts,
	}, nil
}
