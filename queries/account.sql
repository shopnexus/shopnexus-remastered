-- name: GetAccountBase :one
SELECT *
FROM "account".base
WHERE id = $1;

-- name: GetAccountAdmin :one
WITH filtered_roles AS (SELECT r.admin_id,
                               array_agg(r.role_id) as roles
                        FROM "account".role_on_admin r
                        WHERE r.admin_id = $1
                        GROUP BY r.admin_id)
SELECT a.*,
       b.*,
       COALESCE(r.roles, '{}') ::text[] as roles
FROM "account".admin a
       INNER JOIN "account".base b ON a.id = b.id
       LEFT JOIN filtered_roles r ON r.admin_id = a.id
WHERE (
        a.id = sqlc.narg('id') OR
        b.username = sqlc.narg('username')
        );

-- name: GetAccountUser :one
SELECT u.*, b.*
FROM "account".user u
       INNER JOIN "account".base b ON u.id = b.id
WHERE (
        u.id = sqlc.narg('id') OR
        u.email = sqlc.narg('email') OR
        u.phone = sqlc.narg('phone') OR
        b.username = sqlc.narg('username')
        );

-- name: CreateAccountUser :one
WITH base AS (
INSERT
INTO "account".base (username, password, type)
VALUES ($1, $2, 'USER')
  RETURNING id
  )
INSERT
INTO "account".user (id, email, phone, gender, full_name)
SELECT id, $3, $4, $5, $6
FROM base RETURNING id;

-- name: CreateAccountAdmin :one
WITH base AS (
INSERT
INTO "account".base (username, password, type)
VALUES ($1, $2, 'ADMIN')
  RETURNING id
  )
INSERT
INTO "account".admin (id)
SELECT id
FROM base RETURNING id;

-- name: GetRolePermissions :one
SELECT array_agg(p.permission_id) as permissions
FROM "account".permission_on_role p
       INNER JOIN "account".role r ON p.role_id = r.id
WHERE r.id = $1;

-- name: GetAdminPermissions :one
SELECT array_agg(DISTINCT p.permission_id)::TEXT[] AS permissions
FROM "account".role_on_admin r
       INNER JOIN "account".permission_on_role p ON r.role_id = p.role_id
WHERE r.admin_id = $1;

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