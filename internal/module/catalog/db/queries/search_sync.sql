-- name: ListStaleSearchSync :many
SELECT id, ref_id, ref_type
FROM catalog.search_sync
WHERE is_stale_embedding = true
ORDER BY date_updated ASC
FOR UPDATE SKIP LOCKED
LIMIT sqlc.arg('limit');

-- name: MarkStaleSearchSync :exec
UPDATE catalog.search_sync
SET is_stale_embedding = true, date_updated = NOW()
WHERE ref_type = sqlc.arg('ref_type') AND ref_id = sqlc.arg('ref_id');

-- name: ClearStaleSearchSyncBatch :batchexec
UPDATE catalog.search_sync
SET is_stale_embedding = false, date_updated = NOW()
WHERE ref_type = sqlc.arg('ref_type') AND ref_id = sqlc.arg('ref_id');
