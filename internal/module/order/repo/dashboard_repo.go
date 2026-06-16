package orderrepo

import (
	"context"
	"time"

	"github.com/google/uuid"
	ordermodel "shopnexus-server/internal/module/order/model"
)

const getSellerOrderStats = `SELECT
    COALESCE(SUM(i."total_amount"), 0)::BIGINT AS total_revenue,
    COUNT(DISTINCT o."id")::BIGINT AS total_orders,
    COALESCE(SUM(i."quantity"), 0)::BIGINT AS items_sold
FROM "order"."order" o
JOIN "order"."item" i ON i."order_id" = o."id"
WHERE o."seller_id" = $1
    AND i."date_cancelled" IS NULL
    AND i."date_created" BETWEEN $2::TIMESTAMPTZ AND $3::TIMESTAMPTZ`

// GetSellerOrderStatsParams holds the filter args for GetSellerOrderStats.
type GetSellerOrderStatsParams struct {
	SellerID uuid.UUID `json:"seller_id"`
	StartAt  time.Time `json:"start_at"`
	EndAt    time.Time `json:"end_at"`
}

// GetSellerOrderStats aggregates revenue, order count, and items sold for a seller within a date range.
func (r *Repository) GetSellerOrderStats(ctx context.Context, arg GetSellerOrderStatsParams) (ordermodel.SellerOrderStats, error) {
	row := r.db.QueryRow(ctx, getSellerOrderStats, arg.SellerID, arg.StartAt, arg.EndAt)
	var i ordermodel.SellerOrderStats
	err := row.Scan(&i.TotalRevenue, &i.TotalOrders, &i.ItemsSold)
	return i, err
}

const getSellerOrderTimeSeries = `SELECT
    date_trunc($1::text, i."date_created")::TIMESTAMPTZ AS bucket,
    COALESCE(SUM(i."total_amount"), 0)::BIGINT AS revenue,
    COUNT(DISTINCT o."id")::BIGINT AS order_count
FROM "order"."order" o
JOIN "order"."item" i ON i."order_id" = o."id"
WHERE o."seller_id" = $2
    AND i."date_cancelled" IS NULL
    AND i."date_created" BETWEEN $3::TIMESTAMPTZ AND $4::TIMESTAMPTZ
GROUP BY bucket
ORDER BY bucket ASC`

// GetSellerOrderTimeSeriesParams holds the filter args for GetSellerOrderTimeSeries.
// Granularity must be 'day', 'week', or 'month'.
type GetSellerOrderTimeSeriesParams struct {
	Granularity string    `json:"granularity"`
	SellerID    uuid.UUID `json:"seller_id"`
	StartAt     time.Time `json:"start_at"`
	EndAt       time.Time `json:"end_at"`
}

// GetSellerOrderTimeSeries returns time-bucketed revenue and order counts.
func (r *Repository) GetSellerOrderTimeSeries(ctx context.Context, arg GetSellerOrderTimeSeriesParams) ([]ordermodel.SellerOrderTimePoint, error) {
	rows, err := r.db.Query(ctx, getSellerOrderTimeSeries,
		arg.Granularity,
		arg.SellerID,
		arg.StartAt,
		arg.EndAt,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ordermodel.SellerOrderTimePoint{}
	for rows.Next() {
		var i ordermodel.SellerOrderTimePoint
		if err = rows.Scan(&i.Bucket, &i.Revenue, &i.OrderCount); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const getSellerPendingActions = `SELECT
    (SELECT COUNT(*)::BIGINT FROM "order"."item" x
     WHERE x."seller_id" = $1
       AND x."order_id" IS NULL
       AND x."date_cancelled" IS NULL) AS pending_items,
    (SELECT COUNT(*)::BIGINT FROM "order"."refund" r
     JOIN "order"."order" o ON o."id" = r."order_id"
     WHERE o."seller_id" = $1
       AND r."status" = 'AwaitingSellerReview') AS pending_refunds`

// GetSellerPendingActions counts unconfirmed incoming items and pending refunds for a seller.
func (r *Repository) GetSellerPendingActions(ctx context.Context, sellerID uuid.UUID) (ordermodel.SellerPendingActions, error) {
	row := r.db.QueryRow(ctx, getSellerPendingActions, sellerID)
	var i ordermodel.SellerPendingActions
	err := row.Scan(&i.PendingItems, &i.PendingRefunds)
	return i, err
}

const getSellerTopProducts = `SELECT
    i."sku_id",
    i."sku_name",
    SUM(i."quantity")::BIGINT AS sold_count,
    SUM(i."total_amount")::BIGINT AS revenue
FROM "order"."item" i
JOIN "order"."order" o ON i."order_id" = o."id"
WHERE o."seller_id" = $1
    AND i."date_cancelled" IS NULL
    AND i."date_created" BETWEEN $2::TIMESTAMPTZ AND $3::TIMESTAMPTZ
GROUP BY i."sku_id", i."sku_name"
ORDER BY sold_count DESC
LIMIT $4::INTEGER`

// GetSellerTopProductsParams holds the filter args for GetSellerTopProducts.
type GetSellerTopProductsParams struct {
	SellerID uuid.UUID `json:"seller_id"`
	StartAt  time.Time `json:"start_at"`
	EndAt    time.Time `json:"end_at"`
	TopLimit int32     `json:"top_limit"`
}

// GetSellerTopProducts returns top products by sold quantity within a date range.
func (r *Repository) GetSellerTopProducts(ctx context.Context, arg GetSellerTopProductsParams) ([]ordermodel.SellerTopProduct, error) {
	rows, err := r.db.Query(ctx, getSellerTopProducts,
		arg.SellerID,
		arg.StartAt,
		arg.EndAt,
		arg.TopLimit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ordermodel.SellerTopProduct{}
	for rows.Next() {
		var i ordermodel.SellerTopProduct
		if err = rows.Scan(&i.SkuID, &i.SkuName, &i.SoldCount, &i.Revenue); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
