-- name: ListRating :many
SELECT ref_id, AVG(score) as score, COUNT(*) as count
FROM "catalog"."comment"
WHERE (
    ref_type = sqlc.arg('ref_type') AND
    ref_id = ANY(sqlc.slice('ref_id'))
)
GROUP BY ref_id;

-- name: DetailRating :one
SELECT ref_id, AVG(score) as score, COUNT(*) as count,
    COUNT(CASE WHEN score > 0.8 THEN 1 END) as five_count,
    COUNT(CASE WHEN score > 0.6 AND score <= 0.8 THEN 1 END) as four_count,
    COUNT(CASE WHEN score > 0.4 AND score <= 0.6 THEN 1 END) as three_count,
    COUNT(CASE WHEN score > 0.2 AND score <= 0.4 THEN 1 END) as two_count,
    COUNT(CASE WHEN score > 0 AND score <= 0.2 THEN 1 END) as one_count
FROM "catalog"."comment"
WHERE (
    ref_type = sqlc.arg('ref_type') AND
    ref_id = sqlc.arg('ref_id')
)
GROUP BY ref_id;

-- name: SearchCountProductSpu :many
SELECT sqlc.embed(embed_product_spu), COUNT(*) OVER() as total_count
FROM "catalog"."product_spu" embed_product_spu
WHERE (
    ("id" = ANY(sqlc.slice('id')) OR sqlc.slice('id') IS NULL) AND
    ("account_id" = ANY(sqlc.slice('account_id')) OR sqlc.slice('account_id') IS NULL) AND
    ("category_id" = ANY(sqlc.slice('category_id')) OR sqlc.slice('category_id') IS NULL) AND
    ("featured_sku_id" = ANY(sqlc.slice('featured_sku_id')) OR sqlc.slice('featured_sku_id') IS NULL) AND
    ("is_enabled" = ANY(sqlc.slice('is_enabled')) OR sqlc.slice('is_enabled') IS NULL) AND
    ("date_created" = ANY(sqlc.slice('date_created')) OR sqlc.slice('date_created') IS NULL) AND
    ("date_created" > sqlc.narg('date_created_from') OR sqlc.narg('date_created_from') IS NULL) AND
    ("date_created" < sqlc.narg('date_created_to') OR sqlc.narg('date_created_to') IS NULL) AND
    ("date_updated" = ANY(sqlc.slice('date_updated')) OR sqlc.slice('date_updated') IS NULL) AND
    ("date_updated" > sqlc.narg('date_updated_from') OR sqlc.narg('date_updated_from') IS NULL) AND
    ("date_updated" < sqlc.narg('date_updated_to') OR sqlc.narg('date_updated_to') IS NULL) AND
    (
      ("date_deleted" IS NULL) AND (
        ((catalog.f_unaccent("slug") ILIKE '%' || catalog.f_unaccent(sqlc.narg('slug')) || '%') OR sqlc.narg('slug') IS NULL) AND
        ((catalog.f_unaccent("name") ILIKE '%' || catalog.f_unaccent(sqlc.narg('name')) || '%') OR sqlc.narg('name') IS NULL) AND
        ((catalog.f_unaccent("description") ILIKE '%' || catalog.f_unaccent(sqlc.narg('description')) || '%') OR sqlc.narg('description') IS NULL)
      )
    )
)
ORDER BY "id"
LIMIT sqlc.narg('limit')::int
OFFSET sqlc.narg('offset')::int;

-- name: ListCountProductSpuRecent :many
-- Same as ListCountProductSpu but ordered by date_created DESC (newest first)
SELECT sqlc.embed(embed_product_spu), COUNT(*) OVER() as total_count
FROM "catalog"."product_spu" embed_product_spu
WHERE (
    ("id" = ANY(sqlc.slice('id')) OR sqlc.slice('id') IS NULL) AND
    ("slug" = ANY(sqlc.slice('slug')) OR sqlc.slice('slug') IS NULL) AND
    ("account_id" = ANY(sqlc.slice('account_id')) OR sqlc.slice('account_id') IS NULL) AND
    ("category_id" = ANY(sqlc.slice('category_id')) OR sqlc.slice('category_id') IS NULL) AND
    ("is_enabled" = ANY(sqlc.slice('is_enabled')) OR sqlc.slice('is_enabled') IS NULL) AND
    ("date_deleted" IS NULL)
)
ORDER BY "date_created" DESC
LIMIT sqlc.narg('limit')::int
OFFSET sqlc.narg('offset')::int;
