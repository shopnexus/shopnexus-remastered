-- name: GetAddress :one
SELECT *
FROM "account".address
WHERE (
        id = $1 AND
        (user_id = sqlc.narg('user_id') OR sqlc.narg('user_id') IS NULL)
        );

-- name: CountAddresses :one
SELECT COUNT(*)
FROM "account".address
WHERE (
        (user_id = sqlc.narg('user_id') OR sqlc.narg('user_id') IS NULL) AND
        (full_name ILIKE '%' || sqlc.narg('full_name') || '%' OR sqlc.narg('full_name') IS NULL) AND
        (phone ILIKE '%' || sqlc.narg('phone') || '%' OR sqlc.narg('phone') IS NULL) AND
        (address ILIKE '%' || sqlc.narg('address') || '%' OR sqlc.narg('address') IS NULL) AND
        (city ILIKE '%' || sqlc.narg('city') || '%' OR sqlc.narg('city') IS NULL) AND
        (province ILIKE '%' || sqlc.narg('province') || '%' OR sqlc.narg('province') IS NULL) AND
        (country ILIKE '%' || sqlc.narg('country') || '%' OR sqlc.narg('country') IS NULL)
        );

-- name: ListAddresses :many
SELECT *
FROM "account".address
WHERE (
        (user_id = sqlc.narg('user_id') OR sqlc.narg('user_id') IS NULL) AND
        (full_name ILIKE '%' || sqlc.narg('full_name') || '%' OR sqlc.narg('full_name') IS NULL) AND
        (phone ILIKE '%' || sqlc.narg('phone') || '%' OR sqlc.narg('phone') IS NULL) AND
        (address ILIKE '%' || sqlc.narg('address') || '%' OR sqlc.narg('address') IS NULL) AND
        (city ILIKE '%' || sqlc.narg('city') || '%' OR sqlc.narg('city') IS NULL) AND
        (province ILIKE '%' || sqlc.narg('province') || '%' OR sqlc.narg('province') IS NULL) AND
        (country ILIKE '%' || sqlc.narg('country') || '%' OR sqlc.narg('country') IS NULL)
        )
ORDER BY date_created DESC LIMIT sqlc.arg('limit')
OFFSET sqlc.arg('offset');

-- name: CreateAddress :one
INSERT INTO "account".address (user_id, full_name, phone, address, city, province, country)
VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING *;

-- name: UpdateAddress :one
UPDATE "account".address
SET full_name = COALESCE(sqlc.narg('full_name'), full_name),
    phone     = COALESCE(sqlc.narg('phone'), phone),
    address   = COALESCE(sqlc.narg('address'), address),
    city      = COALESCE(sqlc.narg('city'), city),
    province  = COALESCE(sqlc.narg('province'), province),
    country   = COALESCE(sqlc.narg('country'), country)
WHERE (
        id = $1 AND
        (user_id = sqlc.narg('user_id') OR sqlc.narg('user_id') IS NULL)
        -- TODO: thêm check user_id cho toàn bộ query (user chỉ đc interact của họ)
        ) RETURNING *;

-- name: DeleteAddress :one
DELETE
FROM "account".address
WHERE (
        id = $1 AND
        (user_id = sqlc.narg('user_id') OR sqlc.narg('user_id') IS NULL)
        ) RETURNING *;

