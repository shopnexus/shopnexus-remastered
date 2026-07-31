-- Description: the listing aggregate's optimistic lock and the moderation queue index
-- The listing aggregate's optimistic lock. Every Save writes `WHERE version = @version`
-- and bumps it, so a command built on a stale read is refused instead of overwriting
-- whatever landed in between.
ALTER TABLE "listing" ADD COLUMN IF NOT EXISTS "version" BIGINT NOT NULL DEFAULT 1;

-- The moderation queue is two things at once: a listing waiting for its first publication,
-- and a live listing holding an edit its seller submitted. "listing_pending_created_at_idx"
-- covers only the first half.
CREATE INDEX IF NOT EXISTS "listing_moderation_queue_idx"
    ON "listing" ("created_at")
    WHERE "deleted_at" IS NULL
      AND ("status" = 'pending' OR "pending_edit" IS NOT NULL);
