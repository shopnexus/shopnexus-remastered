-- name: ExistsCartItems :one
SELECT EXISTS(
    SELECT 1 FROM account.cart_item WHERE sku_id = ANY(sqlc.arg('sku_ids')::bigint[])
) AS "exists";