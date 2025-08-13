-- name: GetProductModel :one
WITH filtered_model AS (SELECT pm.*
                        FROM product.model pm
                        WHERE pm.id = sqlc.arg('id')),
     filtered_resources AS (SELECT res.owner_id,
                                   array_agg(res.url ORDER BY res.order ASC) AS resources
                            FROM product.resource res
                            WHERE res.owner_id = sqlc.arg('id')
                              AND res.type = 'PRODUCT_MODEL'
                            GROUP BY res.owner_id),
     filtered_tags AS (SELECT t.product_model_id,
                              array_agg(DISTINCT t.tag) AS tags
                       FROM product.tag_on_product_model t
                       WHERE t.product_model_id = sqlc.arg('id')
                       GROUP BY t.product_model_id)
SELECT pm.*,
       COALESCE(res.resources, '{}')::text[] AS resources, COALESCE(t.tags, '{}') AS tags
FROM filtered_model pm
       LEFT JOIN filtered_resources res ON res.owner_id = pm.id
       LEFT JOIN filtered_tags t ON t.product_model_id = pm.id;

-- name: GetProductSerialIDs :many
SELECT serial_id
FROM product.serial
WHERE product_id = $1;

-- name: CountProductTypes :one
SELECT COUNT(id)
FROM product.type
WHERE (
        (name ILIKE '%' || sqlc.narg('name') || '%' OR sqlc.narg('name') IS NULL)
        );

-- name: ListProductTypes :many
SELECT t.*
FROM product.type t
WHERE (
        (name ILIKE '%' || sqlc.narg('name') || '%' OR sqlc.narg('name') IS NULL)
        )
ORDER BY t.id DESC LIMIT sqlc.arg('limit')
OFFSET sqlc.arg('offset');

-- name: CountProductModels :one
SELECT COUNT(id)
FROM product.model pm
WHERE (
        (pm.type = sqlc.narg('type') OR sqlc.narg('type') IS NULL) AND
        (pm.brand_id = sqlc.narg('brand_id') OR sqlc.narg('brand_id') IS NULL) AND
        (pm.name ILIKE '%' || sqlc.narg('name') || '%' OR sqlc.narg('name') IS NULL) AND
        (pm.description ILIKE '%' || sqlc.narg('description') || '%' OR sqlc.narg('description') IS NULL) AND
        (pm.list_price >= sqlc.narg('list_price_from') OR sqlc.narg('list_price_from') IS NULL) AND
        (pm.list_price <= sqlc.narg('list_price_to') OR sqlc.narg('list_price_to') IS NULL) AND
        (pm.date_manufactured >= sqlc.narg('date_manufactured_from') OR sqlc.narg('date_manufactured_from') IS NULL) AND
        (pm.date_manufactured <= sqlc.narg('date_manufactured_to') OR sqlc.narg('date_manufactured_to') IS NULL)
        );

-- name: ListProductModels :many
WITH filtered_models AS (SELECT pm.*
                         FROM product.model pm
                         WHERE (
                                 (pm.type = sqlc.narg('type') OR sqlc.narg('type') IS NULL) AND
                                 (pm.brand_id = sqlc.narg('brand_id') OR sqlc.narg('brand_id') IS NULL) AND
                                 (pm.name ILIKE '%' || sqlc.narg('name') || '%' OR sqlc.narg('name') IS NULL) AND
                                 (pm.description ILIKE '%' || sqlc.narg('description') || '%' OR sqlc.narg('description') IS NULL) AND
                                 (pm.list_price >= sqlc.narg('list_price_from') OR
                                  sqlc.narg('list_price_from') IS NULL) AND
                                 (pm.list_price <= sqlc.narg('list_price_to') OR sqlc.narg('list_price_to') IS NULL) AND
                                 (pm.date_manufactured >= sqlc.narg('date_manufactured_from') OR
                                  sqlc.narg('date_manufactured_from') IS NULL) AND
                                 (pm.date_manufactured <= sqlc.narg('date_manufactured_to') OR
                                  sqlc.narg('date_manufactured_to') IS NULL)
                                 )),
     filtered_resources AS (SELECT res.owner_id,
                                   array_agg(res.url ORDER BY res.order ASC) AS resources
                            FROM product.resource res
                            WHERE res.owner_id IN (SELECT id FROM filtered_models)
                              AND res.type = 'PRODUCT_MODEL'
                            GROUP BY res.owner_id),
     filtered_tags AS (SELECT t.product_model_id,
                              array_agg(DISTINCT t.tag) AS tags
                       FROM product.tag_on_product_model t
                       WHERE t.product_model_id IN (SELECT id FROM filtered_models)
                       GROUP BY t.product_model_id)
SELECT pm.*,
       COALESCE(res.resources, '{}')::text[] AS resources, COALESCE(t.tags, '{}') AS tags
FROM filtered_models pm
       LEFT JOIN filtered_resources res ON res.owner_id = pm.id
       LEFT JOIN filtered_tags t ON t.product_model_id = pm.id
ORDER BY pm.date_manufactured DESC LIMIT sqlc.arg('limit')
OFFSET sqlc.arg('offset');

-- name: CreateProductModel :one
INSERT INTO product.model (type, brand_id, name, description, list_price, date_manufactured)
VALUES ($1, $2, $3, $4, $5, $6) RETURNING *;

-- name: UpdateProductModel :one
UPDATE product.model
SET type              = COALESCE(sqlc.narg('type'), type),
    brand_id          = COALESCE(sqlc.narg('brand_id'), brand_id),
    name              = COALESCE(sqlc.narg('name'), name),
    description       = COALESCE(sqlc.narg('description'), description),
    list_price        = COALESCE(sqlc.narg('list_price'), list_price),
    date_manufactured = COALESCE(sqlc.narg('date_manufactured'), date_manufactured)
WHERE id = $1 RETURNING *;

-- name: DeleteProductModel :exec
DELETE
FROM product.model
WHERE id = $1;

