-- name: LowestPriceProductSku :many
SELECT DISTINCT ON (spu_id) spu_id, id, price
FROM "catalog"."product_sku"
WHERE spu_id = ANY(sqlc.slice('spu_id'))
ORDER BY spu_id, price ASC;

-- name: ListRating :many
SELECT ref_id, AVG(score) as score, COUNT(*) as count
FROM "catalog"."comment"
WHERE (
    ref_type = sqlc.arg('ref_type') AND
    ref_id = ANY(sqlc.slice('ref_id'))
)
GROUP BY ref_id;