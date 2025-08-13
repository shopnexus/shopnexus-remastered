-- name: GetResources :many
SELECT r.url
FROM product.resource r
WHERE r.owner_id = $1
  AND r.type = $2
ORDER BY r.order ASC;

-- name: AddResources :copyfrom
INSERT INTO product.resource (type, owner_id, url, "order")
VALUES ($1, $2, $3, $4);

-- name: EmptyResources :exec
DELETE
FROM product.resource
WHERE owner_id = $1
  AND type = $2;
