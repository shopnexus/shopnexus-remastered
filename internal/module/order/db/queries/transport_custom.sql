-- name: UpdateTransportStatusByID :one
UPDATE "order"."transport"
SET "status" = @status, "data" = @data
WHERE "id" = @id
RETURNING *;

-- name: GetTransportByTrackingID :one
-- data->>'tracking_id' is text; cast the param so sqlc types it as string (not json.RawMessage).
SELECT * FROM "order"."transport"
WHERE "data"->>'tracking_id' = @tracking_id::text
LIMIT 1;

-- name: GetTransportWithOrder :one
SELECT t.*,
       o.id        AS order_id,
       o.buyer_id  AS order_buyer_id,
       o.seller_id AS order_seller_id
FROM "order"."transport" t
INNER JOIN "order"."order" o ON o.transport_id = t.id
WHERE t.id = @id;
