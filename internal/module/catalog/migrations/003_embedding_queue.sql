-- The indexer's work list. Without these, "what is stale" is a sequential scan of the whole
-- table on every pass — and the steady state is a pass that finds nothing, so the cost would
-- be paid entirely by the case where there is no work to do.
--
-- Partial on the marked rows, same shape as "resource_abandoned_idx": the queue is a small hot
-- slice of a table that is mostly fresh. Ordered by the mark, which is the order the indexer
-- drains them in, so a row that has been waiting does not starve behind one somebody edits
-- every minute.
CREATE INDEX IF NOT EXISTS "listing_embedding_stale_idx"
    ON "listing" ("embedding_stale_at", "id")
    WHERE "embedding_stale_at" IS NOT NULL AND "deleted_at" IS NULL;

CREATE INDEX IF NOT EXISTS "category_embedding_stale_idx"
    ON "category" ("embedding_stale_at", "id")
    WHERE "embedding_stale_at" IS NOT NULL;

CREATE INDEX IF NOT EXISTS "tag_embedding_stale_idx"
    ON "tag" ("embedding_stale_at", "id")
    WHERE "embedding_stale_at" IS NOT NULL;
