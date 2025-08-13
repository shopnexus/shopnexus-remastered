-- name: GetBrand :one
WITH filtered_brand AS (SELECT b.*
                        FROM product.brand b
                        WHERE b.id = sqlc.arg('id')),
     filtered_resources AS (SELECT res.owner_id,
                                   array_agg(res.url ORDER BY res.order ASC) AS resources
                            FROM product.resource res
                            WHERE res.owner_id = sqlc.arg('id')
                              AND res.type = 'BRAND'
                            GROUP BY res.owner_id)
SELECT b.*,
       COALESCE(res.resources, '{}') ::text[] AS resources
FROM filtered_brand b
       LEFT JOIN filtered_resources res ON res.owner_id = b.id;

-- name: CountBrands :one
WITH filtered_brands AS (SELECT b.id
                         FROM product.brand b
                         WHERE (
                                 (name ILIKE '%' || sqlc.narg('name') || '%' OR sqlc.narg('name') IS NULL) AND
                                 (description ILIKE '%' || sqlc.narg('description') || '%' OR sqlc.narg('description') IS NULL)
                                 ))
SELECT COUNT(id)
FROM filtered_brands;

-- name: ListBrands :many
WITH filtered_brands AS (SELECT b.*
                         FROM product.brand b
                         WHERE (
                                 (name ILIKE '%' || sqlc.narg('name') || '%' OR sqlc.narg('name') IS NULL) AND
                                 (description ILIKE '%' || sqlc.narg('description') || '%' OR sqlc.narg('description') IS NULL)
                                 )),
     filtered_resources AS (SELECT res.owner_id, array_agg(res.url ORDER BY res.order ASC) AS resources
                            FROM product.resource res
                            WHERE res.owner_id IN (SELECT id FROM filtered_brands)
                              AND res.type = 'BRAND'
                            GROUP BY res.owner_id)
SELECT b.*,
       COALESCE(res.resources, '{}') ::text[] AS resources
FROM filtered_brands b
       LEFT JOIN filtered_resources res ON res.owner_id = b.id
ORDER BY b.name DESC LIMIT sqlc.arg('limit')
OFFSET sqlc.arg('offset');

-- name: CreateBrand :one
INSERT INTO product.brand (name, description)
VALUES ($1, $2) RETURNING *;

-- name: UpdateBrand :exec
UPDATE product.brand
SET name        = COALESCE(sqlc.narg('name'), name),
    description = COALESCE(sqlc.narg('description'), description)
WHERE id = $1;

-- name: DeleteBrand :exec
DELETE
FROM product.brand
WHERE id = $1;