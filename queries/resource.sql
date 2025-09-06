-- name: ListSharedResourceFirst :many
SELECT DISTINCT on (owner_id) url, owner_id
FROM "shared"."resource"
WHERE
    owner_type = sqlc.arg('owner_type') AND
    owner_id = ANY(sqlc.slice('owner_id'))
ORDER BY "owner_id", "order" ASC;
