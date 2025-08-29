-- name: GetProduct :one
WITH filtered_product AS (SELECT p.*, pt.current_stock, pt.sold
                          FROM product.base p
                                   LEFT JOIN product.tracking pt ON p.id = pt.product_id
                          WHERE p.id = sqlc.arg('id')),
     filtered_resources AS (SELECT res.owner_id,
                                   array_agg(res.url ORDER BY res.order ASC) AS resources
                            FROM product.resource res
                            WHERE res.owner_id = sqlc.arg('id')
                              AND res.type = 'PRODUCT'
                            GROUP BY res.owner_id)
SELECT p.*,
       pt.*,
       COALESCE(res.resources, '{}') ::text[] AS resources
FROM filtered_product p
         INNER JOIN product.tracking pt ON p.id = pt.product_id
         LEFT JOIN filtered_resources res ON res.owner_id = p.id;

-- name: CountProducts :one
SELECT COUNT(id)
FROM product.base p
         LEFT JOIN product.tracking pt ON p.id = pt.product_id
WHERE (
          (p.id = sqlc.narg('id') OR sqlc.narg('id') IS NULL) AND
          (product_model_id = sqlc.narg('product_model_id') OR sqlc.narg('product_model_id') IS NULL) AND
          (pt.current_stock >= sqlc.narg('current_stock_from') OR sqlc.narg('current_stock_from') IS NULL) AND
          (pt.current_stock <= sqlc.narg('current_stock_to') OR sqlc.narg('current_stock_to') IS NULL) AND
          (pt.sold >= sqlc.narg('sold_from') OR sqlc.narg('sold_from') IS NULL) AND
          (pt.sold <= sqlc.narg('sold_to') OR sqlc.narg('sold_to') IS NULL) AND
          (additional_price >= sqlc.narg('additional_price_from') OR sqlc.narg('additional_price_from') IS NULL) AND
          (additional_price <= sqlc.narg('additional_price_to') OR sqlc.narg('additional_price_to') IS NULL) AND
          (is_active = sqlc.narg('is_active') OR sqlc.narg('is_active') IS NULL) AND
          (can_combine = sqlc.narg('can_combine') OR sqlc.narg('can_combine') IS NULL) AND
          (metadata @> sqlc.narg('metadata') OR sqlc.narg('metadata') IS NULL) AND
          (date_created >= sqlc.narg('date_created_from') OR sqlc.narg('date_created_from') IS NULL) AND
          (date_created <= sqlc.narg('date_created_to') OR sqlc.narg('date_created_to') IS NULL)
          );

-- name: ListProducts :many
WITH filtered_product AS (SELECT p.*, pt.current_stock, pt.sold
                          FROM product.base p
                                   LEFT JOIN product.tracking pt ON p.id = pt.product_id
                          WHERE (
                                    (p.id = sqlc.narg('id') OR sqlc.narg('id') IS NULL) AND
                                    (p.product_model_id = sqlc.narg('product_model_id') OR
                                     sqlc.narg('product_model_id') IS NULL) AND
                                    (pt.current_stock >= sqlc.narg('current_stock_from') OR
                                     sqlc.narg('current_stock_from') IS NULL) AND
                                    (pt.current_stock <= sqlc.narg('current_stock_to') OR
                                     sqlc.narg('current_stock_to') IS NULL) AND
                                    (pt.sold >= sqlc.narg('sold_from') OR sqlc.narg('sold_from') IS NULL) AND
                                    (pt.sold <= sqlc.narg('sold_to') OR sqlc.narg('sold_to') IS NULL) AND
                                    (p.additional_price >= sqlc.narg('additional_price_from') OR
                                     sqlc.narg('additional_price_from') IS NULL) AND
                                    (p.additional_price <= sqlc.narg('additional_price_to') OR
                                     sqlc.narg('additional_price_to') IS NULL) AND
                                    (p.is_active = sqlc.narg('is_active') OR sqlc.narg('is_active') IS NULL) AND
                                    (p.can_combine = sqlc.narg('can_combine') OR sqlc.narg('can_combine') IS NULL) AND
                                    (p.metadata @> sqlc.narg('metadata') OR sqlc.narg('metadata') IS NULL) AND
                                    (p.date_created >= sqlc.narg('date_created_from') OR
                                     sqlc.narg('date_created_from') IS NULL) AND
                                    (p.date_created <= sqlc.narg('date_created_to') OR
                                     sqlc.narg('date_created_to') IS NULL)
                                    )),
     filtered_resources AS (SELECT res.owner_id,
                                   array_agg(res.url ORDER BY res.order ASC) AS resources
                            FROM product.resource res
                            WHERE res.owner_id IN (SELECT id FROM filtered_product)
                              AND res.type = 'PRODUCT'
                            GROUP BY res.owner_id)
SELECT p.*,
       pt.*,
       COALESCE(res.resources, '{}') ::text[] AS resources
FROM filtered_product p
         INNER JOIN product.tracking pt ON p.id = pt.product_id
         LEFT JOIN filtered_resources res ON res.owner_id = p.id
ORDER BY date_created DESC LIMIT sqlc.arg('limit')
OFFSET sqlc.arg('offset');

-- name: CreateProduct :one
WITH new_product AS (
INSERT
INTO product.base (product_model_id,
                   additional_price,
                   is_active,
                   can_combine,
                   metadata)
VALUES ($1, $2, $3, $4, $5)
    RETURNING *
    )
INSERT
INTO product.tracking (product_id, current_stock, sold)
SELECT new_product.id, $6, 0
FROM new_product RETURNING *;

-- name: UpdateProduct :exec
UPDATE product.base
SET product_model_id = COALESCE(sqlc.narg('product_model_id'), product_model_id),
    additional_price = COALESCE(sqlc.narg('additional_price'), additional_price),
    can_combine      = COALESCE(sqlc.narg('can_combine'), can_combine),
    is_active        = COALESCE(sqlc.narg('is_active'), is_active),
    metadata         = COALESCE(sqlc.narg('metadata'), metadata)
WHERE id = $1;

-- name: UpdateProductTracking :exec
UPDATE product.tracking
SET current_stock = COALESCE(sqlc.narg('current_stock'), current_stock),
    sold          = COALESCE(sqlc.narg('sold'), sold)
WHERE product_id = $1;

-- name: UpdateProductSold :exec
UPDATE product.tracking
SET current_stock = current_stock - sqlc.arg('amount'),
    sold          = sold + sqlc.arg('amount')
WHERE (product_id = ANY (sqlc.arg('ids')::bigint[]))
  AND (current_stock - sqlc.arg('amount') >= 0);

-- name: DeleteProduct :exec
DELETE
FROM product.base
WHERE id = $1;

-- name: GetProductByPOPID :one
WITH filtered_resources AS (SELECT res.owner_id,
                                   array_agg(res.url ORDER BY res.order ASC) AS resources
                            FROM product.resource res
                            WHERE res.owner_id = sqlc.arg('id')
                              AND res.type = 'PRODUCT'
                            GROUP BY res.owner_id)
SELECT p.*,
       COALESCE(res.resources, '{}') ::text[] AS resources
FROM product.base p
         LEFT JOIN filtered_resources res ON res.owner_id = p.id
         INNER JOIN payment.product_on_payment pop ON p.id = pop.product_id
WHERE pop.id = sqlc.arg('id');