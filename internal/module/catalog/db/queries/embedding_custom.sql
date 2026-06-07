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

-- name: HybridSearchProduct :many
-- Dense + sparse ANN with weighted score fusion; scalar filters join live
-- product tables (replaces Milvus denormalized scalars + WeightedReranker).
WITH dense AS (
    SELECT spu_id, 1 - (embedding <=> sqlc.arg('query_dense')::vector) AS dscore
    FROM catalog.product_embedding
    WHERE embedding IS NOT NULL
    ORDER BY embedding <=> sqlc.arg('query_dense')::vector
    LIMIT sqlc.arg('pool')::int
),
sparse AS (
    SELECT spu_id, -(sparse <#> sqlc.narg('query_sparse')::sparsevec) AS sscore
    FROM catalog.product_embedding
    WHERE sqlc.narg('query_sparse')::sparsevec IS NOT NULL AND sparse IS NOT NULL
    ORDER BY sparse <#> sqlc.narg('query_sparse')::sparsevec
    LIMIT sqlc.arg('pool')::int
)
SELECT spu.id,
    (sqlc.arg('dense_weight')::float4 * COALESCE(d.dscore, 0)
   + sqlc.arg('sparse_weight')::float4 * COALESCE(s.sscore, 0))::float4 AS score
FROM catalog.product_spu spu
LEFT JOIN dense d ON d.spu_id = spu.id
LEFT JOIN sparse s ON s.spu_id = spu.id
WHERE (d.spu_id IS NOT NULL OR s.spu_id IS NOT NULL)
  AND spu.date_deleted IS NULL
  AND (spu.account_id = ANY(sqlc.slice('account_id')) OR sqlc.slice('account_id') IS NULL)
  AND (spu.category_id = ANY(sqlc.slice('category_id')) OR sqlc.slice('category_id') IS NULL)
  AND (spu.is_enabled = sqlc.narg('is_enabled') OR sqlc.narg('is_enabled') IS NULL)
  AND (spu.date_created >= sqlc.narg('date_created_from') OR sqlc.narg('date_created_from') IS NULL)
  AND (spu.date_created <= sqlc.narg('date_created_to') OR sqlc.narg('date_created_to') IS NULL)
  AND (EXISTS (
        SELECT 1 FROM catalog.product_sku sku
        WHERE sku.spu_id = spu.id AND sku.date_deleted IS NULL AND sku.price >= sqlc.narg('price_min')
      ) OR sqlc.narg('price_min') IS NULL)
  AND (EXISTS (
        SELECT 1 FROM catalog.product_sku sku
        WHERE sku.spu_id = spu.id AND sku.date_deleted IS NULL AND sku.price <= sqlc.narg('price_max')
      ) OR sqlc.narg('price_max') IS NULL)
  AND (EXISTS (
        SELECT 1 FROM catalog.product_spu_tag pt
        WHERE pt.spu_id = spu.id AND pt.tag = ANY(sqlc.slice('tags'))
      ) OR sqlc.slice('tags') IS NULL)
ORDER BY score DESC
LIMIT sqlc.arg('limit')::int OFFSET sqlc.arg('offset')::int;

-- name: SearchProductByVector :many
-- Single dense ANN over active products (used per interest slot for recommendations).
SELECT pe.spu_id, (1 - (pe.embedding <=> sqlc.arg('query')::vector))::float4 AS score
FROM catalog.product_embedding pe
JOIN catalog.product_spu spu ON spu.id = pe.spu_id
WHERE pe.embedding IS NOT NULL AND spu.is_enabled = true AND spu.date_deleted IS NULL
ORDER BY pe.embedding <=> sqlc.arg('query')::vector
LIMIT sqlc.arg('limit')::int;
