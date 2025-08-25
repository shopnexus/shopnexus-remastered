-- name: ExistsCart :one
SELECT EXISTS (SELECT 1
               FROM "account".cart
               WHERE id = $1);

-- name: CreateCart :exec
INSERT INTO "account".cart (id)
VALUES ($1);

-- name: GetCartItems :many
SELECT *
FROM "account".item_on_cart
WHERE cart_id = $1
  AND (
  cardinality(coalesce(sqlc.arg('product_ids')::bigint[], '{}')) = 0 OR
  product_id = ANY (sqlc.arg('product_ids')::bigint[])
  )
ORDER BY date_created DESC;

-- name: AddCartItem :one
INSERT INTO "account".item_on_cart (cart_id, product_id, quantity)
VALUES ($1, $2, $3) ON CONFLICT (cart_id, product_id)
DO
UPDATE SET quantity = "account".item_on_cart.quantity + $3
  RETURNING quantity;

-- name: UpdateCartItem :one
UPDATE "account".item_on_cart
SET quantity = $3
WHERE cart_id = $1
  AND product_id = $2 RETURNING quantity;

-- name: RemoveCartItem :exec
DELETE
FROM "account".item_on_cart
WHERE cart_id = $1
  AND product_id = ANY (sqlc.arg('product_ids')::bigint[]);

-- name: ClearCart :exec
DELETE
FROM "account".item_on_cart
WHERE cart_id = $1;