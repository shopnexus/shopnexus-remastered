-- name: GetAccount :one
SELECT *
FROM "account".account
WHERE id = $1;

-- name: CreateAccount :one
INSERT INTO "account".account (id, code, type, status, phone, email, username, password, date_created, date_updated)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING *;

-- name: CreateProfile :one
INSERT INTO "account".profile (id, account_id, gender, name, date_of_birth, avatar_rs_id, email_verified,
                               phone_verified, date_updated)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING *;

-- name: CreateCustomer :one
INSERT INTO "account".customer (id, account_id, default_address_id, date_updated)
VALUES ($1, $2, $3, $4) RETURNING *;

-- name: CreateVendor :one
INSERT INTO "account".vendor (id, account_id)
VALUES ($1, $2) RETURNING *;

-- name: CreateCartItem :one
INSERT INTO "account".cart_item (id, cart_id, sku_id, quantity, date_created, date_updated)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: CreateAddress :one
INSERT INTO "account".address (id, code, account_id, type, full_name, phone, phone_verified, address_line, city, state_province, country,
                               date_created, date_updated)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING *;

-- name: UpdateAccount :one
UPDATE "account".account
SET code        = COALESCE(sqlc.narg('code'), code),
    type         = COALESCE(sqlc.narg('type'), type),
    status       = COALESCE(sqlc.narg('status'), status),
    phone = CASE WHEN sqlc.narg('null_phone') = TRUE THEN NULL ELSE COALESCE(sqlc.narg('phone'), phone) END,
    email = CASE WHEN sqlc.narg('null_email') = TRUE THEN NULL ELSE COALESCE(sqlc.narg('email'), email) END,
    username  = CASE WHEN sqlc.narg('null_username') = TRUE THEN NULL ELSE COALESCE(sqlc.narg('username'), username) END,
    password     = CASE WHEN sqlc.narg('null_password') = TRUE THEN NULL ELSE COALESCE(sqlc.narg('password'), password) END,
    date_created = COALESCE(sqlc.narg('date_created'), date_created),
    date_updated = COALESCE(sqlc.narg('date_updated'), date_updated)
WHERE id = $1
RETURNING *;

-- name: UpdateProfile :one
UPDATE "account".profile
SET account_id     = COALESCE(sqlc.narg('account_id'), account),
    gender = CASE WHEN sqlc.narg('null_gender') = TRUE THEN NULL ELSE COALESCE(sqlc.narg('gender'), gender) END,
    name = CASE WHEN sqlc.narg('null_name') = TRUE THEN NULL ELSE COALESCE(sqlc.narg('name'), name) END,
    date_of_birth = CASE WHEN sqlc.narg('null_date_of_birth') = TRUE THEN NULL ELSE COALESCE(sqlc.narg('date_of_birth'), date_of_birth) END,
    avatar_rs_id = CASE WHEN sqlc.narg('null_avatar_rs_id') = TRUE THEN NULL ELSE COALESCE(sqlc.narg('avatar_rs_id'), avatar_rs_id) END,
    email_verified = COALESCE(sqlc.narg('email_verified'), email_verified),
    phone_verified = COALESCE(sqlc.narg('phone_verified'), phone_verified),
    date_updated   = COALESCE(sqlc.narg('date_updated'), date_updated)
WHERE id = $1
RETURNING *;

-- name: UpdateCustomer :one
UPDATE "account".customer
SET account_id        = COALESCE(sqlc.narg('account_id'), account_id),
    default_address_id = CASE WHEN sqlc.narg('null_default_address_id') = TRUE THEN NULL ELSE COALESCE(sqlc.narg('default_address_id'), default_address_id) END,
    date_updated       = COALESCE(sqlc.narg('date_updated'), date_updated)
WHERE id = $1
RETURNING *;

-- name: UpdateVendor :one
UPDATE "account".vendor
SET account_id = COALESCE(sqlc.narg('account_id'), account_id)
WHERE id = $1
RETURNING *;

-- name: UpdateCartItem :one
UPDATE "account".cart_item
SET cart_id      = COALESCE(sqlc.narg('cart_id'), cart_id),
    sku_id       = COALESCE(sqlc.narg('sku_id'), sku_id),
    quantity     = COALESCE(sqlc.narg('quantity'), quantity),
    date_created = COALESCE(sqlc.narg('date_created'), date_created),
    date_updated = COALESCE(sqlc.narg('date_updated'), date_updated)
WHERE id = $1
RETURNING *;

-- name: UpdateAddress :one
UPDATE "account".address
SET code         = COALESCE(sqlc.narg('code'), code),
    account_id   = COALESCE(sqlc.narg('account_id'), account_id),
    type         = COALESCE(sqlc.narg('type'), type),
    full_name    = COALESCE(sqlc.narg('full_name'), full_name),
    phone        = COALESCE(sqlc.narg('phone'), phone),
    phone_verified = COALESCE(sqlc.narg('phone_verified'), phone_verified),
    address_line = COALESCE(sqlc.narg('address_line'), address_line),
    city         = COALESCE(sqlc.narg('city'), city),
    state_province = COALESCE(sqlc.narg('state_province'), state_province),
    country      = COALESCE(sqlc.narg('country'), country),
    date_created = COALESCE(sqlc.narg('date_created'), date_created),
    date_updated = COALESCE(sqlc.narg('date_updated'), date_updated)
WHERE id = $1
RETURNING *;

-- name: DeleteAccount :exec
DELETE FROM "account".account
WHERE id = $1;

-- name: DeleteProfile :exec
DELETE FROM "account".profile
WHERE id = $1;

-- name: DeleteCustomer :exec
DELETE FROM "account".customer
WHERE id = $1;

-- name: DeleteVendor :exec
DELETE FROM "account".vendor
WHERE id = $1;

-- name: DeleteCartItem :exec
DELETE FROM "account".cart_item
WHERE id = $1;

-- name: DeleteAddress :exec
DELETE FROM "account".address
WHERE id = $1;