-- Custom dashboard aggregation queries for seller analytics

-- name: GetSellerOrderStats :one
-- Aggregates revenue, order count, and items sold for a seller within a date range.
SELECT
    COALESCE(SUM(i."total_amount"), 0)::BIGINT AS total_revenue,
    COUNT(DISTINCT o."id")::BIGINT AS total_orders,
    COALESCE(SUM(i."quantity"), 0)::BIGINT AS items_sold
FROM "order"."order" o
JOIN "order"."item" i ON i."order_id" = o."id"
WHERE o."seller_id" = @seller_id
    AND i."date_cancelled" IS NULL
    AND i."date_created" BETWEEN @start_at::TIMESTAMPTZ AND @end_at::TIMESTAMPTZ;

-- name: GetSellerOrderTimeSeries :many
-- Returns time-bucketed revenue and order counts for chart data.
-- @granularity must be 'day', 'week', or 'month'.
SELECT
    date_trunc(@granularity::text, i."date_created")::TIMESTAMPTZ AS bucket,
    COALESCE(SUM(i."total_amount"), 0)::BIGINT AS revenue,
    COUNT(DISTINCT o."id")::BIGINT AS order_count
FROM "order"."order" o
JOIN "order"."item" i ON i."order_id" = o."id"
WHERE o."seller_id" = @seller_id
    AND i."date_cancelled" IS NULL
    AND i."date_created" BETWEEN @start_at::TIMESTAMPTZ AND @end_at::TIMESTAMPTZ
GROUP BY bucket
ORDER BY bucket ASC;

-- name: GetSellerPendingActions :one
-- Counts unconfirmed incoming items and pending refunds for a seller.
SELECT
    (SELECT COUNT(*)::BIGINT FROM "order"."item" x
     WHERE x."seller_id" = @seller_id
       AND x."order_id" IS NULL
       AND x."date_cancelled" IS NULL) AS pending_items,
    (SELECT COUNT(*)::BIGINT FROM "order"."refund" r
     JOIN "order"."order" o ON o."id" = r."order_id"
     WHERE o."seller_id" = @seller_id
       AND r."status" = 'AwaitingSellerReview') AS pending_refunds;

-- name: GetSellerTopProducts :many
-- Returns top products by sold quantity within a date range, with revenue.
SELECT
    i."sku_id",
    i."sku_name",
    SUM(i."quantity")::BIGINT AS sold_count,
    SUM(i."total_amount")::BIGINT AS revenue
FROM "order"."item" i
JOIN "order"."order" o ON i."order_id" = o."id"
WHERE o."seller_id" = @seller_id
    AND i."date_cancelled" IS NULL
    AND i."date_created" BETWEEN @start_at::TIMESTAMPTZ AND @end_at::TIMESTAMPTZ
GROUP BY i."sku_id", i."sku_name"
ORDER BY sold_count DESC
LIMIT @top_limit::INTEGER;
