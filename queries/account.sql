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
INSERT INTO "account".address (id, code, account_id, type, full_name, phone, phone_verified, address, city, province, country, is_default,
                               date_created, date_updated)

-- name: UpdateAccount :one
UPDATE "account".base
SET username = COALESCE(sqlc.narg('username'), username),
    password = COALESCE(sqlc.narg('password'), password)
WHERE id = $1 RETURNING *;

-- name: UpdateAccountUser :one
UPDATE "account".user
SET email              = COALESCE(sqlc.narg('email'), email),
    phone              = COALESCE(sqlc.narg('phone'), phone),
    gender             = COALESCE(sqlc.narg('gender'), gender),
    full_name          = COALESCE(sqlc.narg('full_name'), full_name),
    default_address_id = CASE
                             WHEN sqlc.narg('null_default_address_id') = TRUE THEN NULL
                             ELSE COALESCE(sqlc.narg('default_address_id'), default_address_id) END,
    avatar_url         = COALESCE(sqlc.narg('avatar_url'), avatar_url)
WHERE id = $1 RETURNING *;

-- name: UpdateAccountAdmin :one
UPDATE "account".admin
SET avatar_url = COALESCE(sqlc.narg('avatar_url'), avatar_url)
WHERE id = $1 RETURNING *;

-- name: AddAdminRole :exec
INSERT INTO "account".role_on_admin (admin_id, role_id)
VALUES ($1, $2) ON CONFLICT (admin_id, role_id) DO NOTHING;

-- name: RemoveAdminRole :exec
DELETE
FROM "account".role_on_admin
WHERE admin_id = $1
  AND role_id = $2;