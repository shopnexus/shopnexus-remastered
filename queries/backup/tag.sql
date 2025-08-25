-- name: GetTag :one
SELECT *
FROM product.tag
WHERE tag = $1;

-- name: CountTags :one
SELECT COUNT(*)
FROM product.tag
WHERE (sqlc.narg('tag')::text IS NULL OR tag ILIKE '%' || sqlc.narg('tag') || '%')
  AND (sqlc.narg('description')::text IS NULL OR description ILIKE '%' || sqlc.narg('description') || '%');

-- name: ListTags :many
SELECT *
FROM product.tag
WHERE (sqlc.narg('tag')::text IS NULL OR tag ILIKE '%' || sqlc.narg('tag') || '%')
  AND (sqlc.narg('description')::text IS NULL OR description ILIKE '%' || sqlc.narg('description') || '%')
ORDER BY tag LIMIT sqlc.arg('limit')
OFFSET sqlc.arg('offset');


-- name: CreateTag :exec
INSERT INTO product.tag (tag,
                         description)
VALUES ($1, $2);

-- name: UpdateTag :exec
UPDATE product.tag
SET tag         = COALESCE(sqlc.narg('new_tag'), tag),
    description = COALESCE(sqlc.narg('description'), description)
WHERE tag = $1;

-- name: DeleteTag :exec
DELETE
FROM product.tag
WHERE tag = $1;

-- name: CountProductModelsOnTag :one
SELECT COUNT(product_model_id)
FROM product.tag_on_product_model
WHERE tag = $1;

-- name: GetTags :many
SELECT tag
FROM product.tag_on_product_model
WHERE product_model_id = $1;

-- name: AddTags :exec
INSERT INTO product.tag_on_product_model (product_model_id, tag)
SELECT $1,
       unnest(sqlc.arg('tags')::text[]) ON CONFLICT DO NOTHING;

-- name: RemoveTags :exec
DELETE
FROM product.tag_on_product_model
WHERE product_model_id = $1
  AND tag = ANY (sqlc.arg('tags')::text[]);