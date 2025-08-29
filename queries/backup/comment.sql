-- name: GetComment :one
WITH filtered_comment AS (SELECT c.*
                          FROM product.comment c
                          WHERE c.id = sqlc.arg('id')),
     filtered_resources AS (SELECT res.owner_id,
                                   array_agg(res.url ORDER BY res.order ASC) AS resources
                            FROM product.resource res
                            WHERE res.owner_id = sqlc.arg('id')
                              AND res.type = 'COMMENT'
                            GROUP BY res.owner_id)
SELECT c.*,
       COALESCE(res.resources, '{}') ::text[] AS resources
FROM filtered_comment c
       LEFT JOIN filtered_resources res ON res.owner_id = c.id;

-- name: CountComments :one
SELECT COUNT(id)
FROM product.comment
WHERE (account_id = sqlc.narg('account_id') OR sqlc.narg('account_id') IS NULL)
  AND (type = sqlc.narg('type') OR sqlc.narg('type') IS NULL)
  AND (dest_id = sqlc.narg('dest_id') OR sqlc.narg('dest_id') IS NULL)
  AND (body ILIKE '%' || sqlc.narg('body') || '%' OR sqlc.narg('body') IS NULL)
  AND (upvote >= sqlc.narg('upvote_from') OR sqlc.narg('upvote_from') IS NULL)
  AND (upvote <= sqlc.narg('upvote_to') OR sqlc.narg('upvote_to') IS NULL)
  AND (downvote >= sqlc.narg('downvote_from') OR sqlc.narg('downvote_from') IS NULL)
  AND (downvote <= sqlc.narg('downvote_to') OR sqlc.narg('downvote_to') IS NULL)
  AND (score >= sqlc.narg('score_from') OR sqlc.narg('score_from') IS NULL)
  AND (score <= sqlc.narg('score_to') OR sqlc.narg('score_to') IS NULL)
  AND (date_created >= sqlc.narg('created_at_from') OR sqlc.narg('created_at_from') IS NULL)
  AND (date_created <= sqlc.narg('created_at_to') OR sqlc.narg('created_at_to') IS NULL);

-- name: ListComments :many
WITH filtered_comment AS (SELECT c.*
                          FROM product.comment c
                          WHERE (c.account_id = sqlc.narg('account_id') OR sqlc.narg('account_id') IS NULL)
                            AND (c.type = sqlc.narg('type') OR sqlc.narg('type') IS NULL)
                            AND (c.dest_id = sqlc.narg('dest_id') OR sqlc.narg('dest_id') IS NULL)
                            AND (c.body ILIKE '%' || sqlc.narg('body') || '%' OR sqlc.narg('body') IS NULL)
                            AND (c.upvote >= sqlc.narg('upvote_from') OR sqlc.narg('upvote_from') IS NULL)
                            AND (c.upvote <= sqlc.narg('upvote_to') OR sqlc.narg('upvote_to') IS NULL)
                            AND (c.downvote >= sqlc.narg('downvote_from') OR sqlc.narg('downvote_from') IS NULL)
                            AND (c.downvote <= sqlc.narg('downvote_to') OR sqlc.narg('downvote_to') IS NULL)
                            AND (c.score >= sqlc.narg('score_from') OR sqlc.narg('score_from') IS NULL)
                            AND (c.score <= sqlc.narg('score_to') OR sqlc.narg('score_to') IS NULL)
                            AND (c.date_created >= sqlc.narg('created_at_from') OR sqlc.narg('created_at_from') IS NULL)
                            AND (c.date_created <= sqlc.narg('created_at_to') OR sqlc.narg('created_at_to') IS NULL)),
     filtered_resources AS (SELECT res.owner_id,
                                   array_agg(res.url ORDER BY res.order ASC) AS resources
                            FROM product.resource res
                            WHERE res.owner_id IN (SELECT id FROM filtered_comment)
                              AND res.type = 'COMMENT'
                            GROUP BY res.owner_id)
SELECT c.*,
       COALESCE(res.resources, '{}') ::text[] AS resources
FROM filtered_comment c
       LEFT JOIN filtered_resources res ON res.owner_id = c.id
ORDER BY c.date_created DESC LIMIT sqlc.arg('limit')
OFFSET sqlc.arg('offset');

-- name: CreateComment :one
INSERT INTO product.comment (account_id, type, dest_id, body, upvote, downvote, score)
VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING *;

-- name: UpdateComment :one
UPDATE product.comment
SET body     = COALESCE(sqlc.narg('body'), body),
    upvote   = COALESCE(sqlc.narg('upvote'), upvote),
    downvote = COALESCE(sqlc.narg('downvote'), downvote),
    score    = COALESCE(sqlc.narg('score'), score)
WHERE id = $1
  AND (account_id = sqlc.narg('account_id') OR sqlc.narg('account_id') IS NULL) RETURNING *;

-- name: DeleteComment :exec
DELETE
FROM product.comment
WHERE (
        id = $1
          AND (account_id = sqlc.narg('account_id') OR sqlc.narg('account_id') IS NULL)
        );