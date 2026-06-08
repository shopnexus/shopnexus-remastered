-- Hand-written vector queries (pgvector). Embedding upserts, hybrid search,
-- and per-interest-slot ANN. Generated CRUD for these tables is unused.

-- name: UpsertProductEmbedding :batchexec
INSERT INTO catalog.product_embedding (spu_id, embedding, sparse, date_updated)
VALUES (sqlc.arg('spu_id'), sqlc.narg('embedding'), sqlc.narg('sparse'), NOW())
ON CONFLICT (spu_id) DO UPDATE SET
    embedding = EXCLUDED.embedding,
    sparse = EXCLUDED.sparse,
    date_updated = NOW();

-- name: UpsertCategoryEmbedding :batchexec
INSERT INTO catalog.category_embedding (category_id, embedding, sparse, date_updated)
VALUES (sqlc.arg('category_id'), sqlc.narg('embedding'), sqlc.narg('sparse'), NOW())
ON CONFLICT (category_id) DO UPDATE SET
    embedding = EXCLUDED.embedding,
    sparse = EXCLUDED.sparse,
    date_updated = NOW();

-- name: UpsertTagEmbedding :batchexec
INSERT INTO catalog.tag_embedding (tag_id, embedding, sparse, date_updated)
VALUES (sqlc.arg('tag_id'), sqlc.narg('embedding'), sqlc.narg('sparse'), NOW())
ON CONFLICT (tag_id) DO UPDATE SET
    embedding = EXCLUDED.embedding,
    sparse = EXCLUDED.sparse,
    date_updated = NOW();

-- name: GetProductVectors :many
SELECT spu_id, embedding
FROM catalog.product_embedding
WHERE spu_id = ANY(sqlc.arg('spu_ids')::uuid[]) AND embedding IS NOT NULL;

-- name: ListAccountInterest :many
SELECT account_id, slot, embedding, strength
FROM catalog.account_interest
WHERE account_id = ANY(sqlc.arg('account_ids')::uuid[])
ORDER BY account_id, slot;

-- name: UpsertAccountInterest :batchexec
INSERT INTO catalog.account_interest (account_id, slot, embedding, strength, date_updated)
VALUES (sqlc.arg('account_id'), sqlc.arg('slot'), sqlc.arg('embedding'), sqlc.arg('strength'), NOW())
ON CONFLICT (account_id, slot) DO UPDATE SET
    embedding = EXCLUDED.embedding,
    strength = EXCLUDED.strength,
    date_updated = NOW();

-- HybridSearchProduct is hand-written (CTEs + score fusion + pagination/count);
-- see internal/module/catalog/db/sqlc/embedding_search.go.

-- name: SearchProductByVector :many
-- Single dense ANN over active products (used per interest slot for recommendations).
SELECT pe.spu_id, (1 - (pe.embedding <=> sqlc.arg('query')::vector))::float4 AS score
FROM catalog.product_embedding pe
JOIN catalog.product_spu spu ON spu.id = pe.spu_id
WHERE pe.embedding IS NOT NULL AND spu.is_enabled = true AND spu.date_deleted IS NULL
ORDER BY pe.embedding <=> sqlc.arg('query')::vector
LIMIT sqlc.arg('limit')::int;
