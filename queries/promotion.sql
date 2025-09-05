-- name: ListActivePromotion :many
SELECT *
FROM promotion.base
WHERE is_active = true
  AND (date_ended IS NULL OR date_ended > NOW())
  AND ("ref_type" = (sqlc.narg('ref_type')) OR sqlc.slice('ref_type') IS NULL)
  AND ("ref_id" = ANY(sqlc.narg('ref_id')) OR sqlc.narg('ref_id') IS NULL)
  AND ("type" = ANY(sqlc.slice('type')) OR sqlc.slice('type') IS NULL);
