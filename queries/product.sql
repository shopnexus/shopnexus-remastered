-- name: LowestPriceProductSku :many
SELECT DISTINCT ON (spu_id) spu_id, id, price
FROM "catalog"."product_sku"
WHERE spu_id = ANY(sqlc.slice('spu_id'))
ORDER BY spu_id, price ASC;