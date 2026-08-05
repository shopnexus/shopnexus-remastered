-- Why a hidden listing is hidden.
--
-- `Takedown` (a moderator) and `Hide` (the seller) both wrote `status = 'hidden'` and nothing else,
-- so a seller whose listing was removed for counterfeiting saw exactly what a seller who hid their
-- own listing saw: no marker, no reason. The decision was in the audit trail, which is the right
-- place for history and the wrong place for a screen.

ALTER TABLE "listing"
    -- Set by a takedown and by nothing else, so its presence is what says "staff did this".
    ADD COLUMN IF NOT EXISTS "taken_down_at" TIMESTAMPTZ,
    -- The seller-visible words. NULL when the moderator chose not to tell them, which is what the
    -- `notify_seller` flag has always meant; the full reason is in the trail either way.
    ADD COLUMN IF NOT EXISTS "takedown_reason" TEXT;

-- A reason belongs to a takedown.
ALTER TABLE "listing"
    DROP CONSTRAINT IF EXISTS "listing_takedown_reason_needs_takedown";
ALTER TABLE "listing"
    ADD CONSTRAINT "listing_takedown_reason_needs_takedown"
    CHECK ("takedown_reason" IS NULL OR "taken_down_at" IS NOT NULL);

-- And a takedown only describes a listing that is currently down. Publishing from hidden re-enters
-- moderation, so the transition clears these; the CHECK is what stops a stale reason surviving it
-- and telling a seller their live listing was removed.
ALTER TABLE "listing"
    DROP CONSTRAINT IF EXISTS "listing_takedown_only_while_hidden";
ALTER TABLE "listing"
    ADD CONSTRAINT "listing_takedown_only_while_hidden"
    CHECK ("taken_down_at" IS NULL OR "status" = 'hidden');
