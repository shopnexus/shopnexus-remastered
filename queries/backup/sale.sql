-- name: CountSales :one
SELECT COUNT(*)
FROM product.sale s
       LEFT JOIN product.sale_tracking st ON s.id = st.sale_id
WHERE (sqlc.narg('type')::text IS NULL OR s.type = sqlc.narg('type'))
  AND (sqlc.narg('item_id')::bigint IS NULL OR s.item_id = sqlc.narg('item_id'))
  AND (sqlc.narg('date_started_from')::timestamptz IS NULL OR s.date_started >= sqlc.narg('date_started_from'))
  AND (sqlc.narg('date_started_to')::timestamptz IS NULL OR s.date_started <= sqlc.narg('date_started_to'))
  AND (sqlc.narg('date_ended_from')::timestamptz IS NULL OR s.date_ended >= sqlc.narg('date_ended_from'))
  AND (sqlc.narg('date_ended_to')::timestamptz IS NULL OR s.date_ended <= sqlc.narg('date_ended_to'))
  AND (sqlc.narg('is_active')::boolean IS NULL OR s.is_active = sqlc.narg('is_active'));

-- name: ListSales :many
SELECT s.*, st.current_stock, st.used
FROM product.sale s
       LEFT JOIN product.sale_tracking st ON s.id = st.sale_id
WHERE (sqlc.narg('type')::text IS NULL OR s.type = sqlc.narg('type'))
  AND (sqlc.narg('item_id')::bigint IS NULL OR s.item_id = sqlc.narg('item_id'))
  AND (sqlc.narg('date_started_from')::timestamptz IS NULL OR s.date_started >= sqlc.narg('date_started_from'))
  AND (sqlc.narg('date_started_to')::timestamptz IS NULL OR s.date_started <= sqlc.narg('date_started_to'))
  AND (sqlc.narg('date_ended_from')::timestamptz IS NULL OR s.date_ended >= sqlc.narg('date_ended_from'))
  AND (sqlc.narg('date_ended_to')::timestamptz IS NULL OR s.date_ended <= sqlc.narg('date_ended_to'))
  AND (sqlc.narg('is_active')::boolean IS NULL OR s.is_active = sqlc.narg('is_active'))
ORDER BY s.id LIMIT sqlc.arg('limit')
OFFSET sqlc.arg('offset');

-- name: GetSale :one
SELECT s.*, st.current_stock, st.used
FROM product.sale s
       LEFT JOIN product.sale_tracking st ON s.id = st.sale_id
WHERE s.id = $1;

-- name: GetAvailableSales :many
SELECT s.*, st.current_stock, st.used
FROM product.sale s
       LEFT JOIN product.sale_tracking st ON s.id = st.sale_id
WHERE s.is_active = true
  AND st.current_stock > 0
  AND s.date_started <= CURRENT_TIMESTAMP
  AND (s.date_ended IS NULL OR s.date_ended >= CURRENT_TIMESTAMP)
  AND (
  (s.type = 'PRODUCT_MODEL' AND s.item_id = sqlc.arg('product_model_id')::bigint) OR
  (s.type = 'BRAND' AND s.item_id = sqlc.arg('brand_id')::bigint) OR
  (s.type = 'TAG' AND s.item_id IN (SELECT product_model_id
                                    FROM product.tag_on_product_model
                                    WHERE tag = ANY (sqlc.arg('tags')::text[])))
  );

-- name: CreateSale :one
WITH new_sale AS (
INSERT
INTO product.sale (type,
                   item_id,
                   date_started,
                   date_ended,
                   is_active,
                   discount_percent,
                   discount_price,
                   max_discount_price)
VALUES (
  $1, $2, $3, $4, $5, $6, $7, $8
  ) RETURNING *
  ), new_sale_tracking AS (
INSERT
INTO product.sale_tracking (sale_id, current_stock, used)
SELECT id, $9, 0
FROM new_sale
  RETURNING *
  )
SELECT ns.*, nst.current_stock, nst.used
FROM new_sale ns
       JOIN new_sale_tracking nst ON ns.id = nst.sale_id;

-- name: UpdateSale :exec
UPDATE product.sale
SET type               = COALESCE(sqlc.narg('type'), type),
    item_id            = COALESCE(sqlc.narg('item_id'), item_id),
    date_started       = COALESCE(sqlc.narg('date_started'), date_started),
    date_ended         = COALESCE(sqlc.narg('date_ended'), date_ended),
    is_active          = COALESCE(sqlc.narg('is_active'), is_active),
    discount_percent   = COALESCE(sqlc.narg('discount_percent'), discount_percent),
    discount_price     = COALESCE(sqlc.narg('discount_price'), discount_price),
    max_discount_price = COALESCE(sqlc.narg('max_discount_price'), max_discount_price)
WHERE id = $1;

-- name: UpdateSaleTracking :exec
UPDATE product.sale_tracking
SET current_stock = COALESCE(sqlc.narg('current_stock'), current_stock),
    used          = COALESCE(sqlc.narg('used'), used)
WHERE sale_id = $1;

-- name: DeleteSale :exec
DELETE
FROM product.sale
WHERE id = $1;
