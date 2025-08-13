-- name: GetProductSerial :one
SELECT *
FROM product.serial
WHERE serial_id = $1;

-- name: ListProductSerials :many
SELECT *
FROM product.serial
WHERE (
        (serial_id ILIKE '%' || sqlc.narg('serial_id') || '%' OR sqlc.narg('serial_id') IS NULL) AND
        (product_id = sqlc.narg('product_id') OR sqlc.narg('product_id') IS NULL) AND
        (is_sold = sqlc.narg('is_sold') OR sqlc.narg('is_sold') IS NULL) AND
        (is_active = sqlc.narg('is_active') OR sqlc.narg('is_active') IS NULL) AND
        (date_created >= sqlc.narg('date_created_from') OR sqlc.narg('date_created_from') IS NULL) AND
        (date_created <= sqlc.narg('date_created_to') OR sqlc.narg('date_created_to') IS NULL)
        )
ORDER BY date_created DESC LIMIT sqlc.arg('limit')
OFFSET sqlc.arg('offset');

-- name: CountProductSerials :one
SELECT COUNT(serial_id)
FROM product.serial
WHERE (
        (serial_id ILIKE '%' || sqlc.narg('serial_id') || '%' OR sqlc.narg('serial_id') IS NULL) AND
        (product_id = sqlc.narg('product_id') OR sqlc.narg('product_id') IS NULL) AND
        (is_sold = sqlc.narg('is_sold') OR sqlc.narg('is_sold') IS NULL) AND
        (is_active = sqlc.narg('is_active') OR sqlc.narg('is_active') IS NULL) AND
        (date_created >= sqlc.narg('date_created_from') OR sqlc.narg('date_created_from') IS NULL) AND
        (date_created <= sqlc.narg('date_created_to') OR sqlc.narg('date_created_to') IS NULL)
        );

-- name: CreateProductSerial :one
INSERT INTO product.serial (serial_id,
                            product_id,
                            is_sold,
                            is_active)
VALUES ($1, $2, $3, $4) RETURNING *;

-- name: UpdateProductSerial :exec
UPDATE product.serial
SET is_sold   = COALESCE(sqlc.narg('is_sold'), is_sold),
    is_active = COALESCE(sqlc.narg('is_active'), is_active)
WHERE serial_id = $1;

-- name: DeleteProductSerial :exec
DELETE
FROM product.serial
WHERE serial_id = $1;

-- name: MarkProductSerialsAsSold :exec
UPDATE product.serial
SET is_sold = true
WHERE serial_id = ANY (sqlc.arg('serial_ids')::text[]);

-- name: GetAvailableProducts :many
SELECT *
FROM product.serial
WHERE (
        product_id = $1 AND
        is_sold = false AND
        is_active = true
        ) LIMIT sqlc.arg('amount');