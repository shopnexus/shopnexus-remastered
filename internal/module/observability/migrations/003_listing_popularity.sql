-- listing_popularity is the platform-wide engagement score for one listing, folded in by
-- subscribeInteractions from catalog.listing_interaction — the same fact catalog's own
-- subscriber turns into a per-account listing_signal row. No FK to catalog.listing: the two
-- live in different schemas (and may, later, different databases), so a listing_id here is a
-- number this module trusts rather than one it can check.
CREATE TABLE IF NOT EXISTS "listing_popularity" (
    "listing_id" BIGINT NOT NULL,
    -- The weighted composite: what a feed ranks by. Can go negative — a listing everybody
    -- hides is a listing everybody hides — and nothing here floors it at zero.
    "score" DOUBLE PRECISION NOT NULL DEFAULT 0,
    "view_count" BIGINT NOT NULL DEFAULT 0,
    -- Every click-from-* type folds into this one counter: the score already carries which
    -- kind mattered more, and a page reading "trending" has no use for three separate numbers.
    "click_count" BIGINT NOT NULL DEFAULT 0,
    -- "not-interested" and "hidden" together — the two catalog's own interest average refuses
    -- to hold, and the only reason this table's score can go negative.
    "dismiss_count" BIGINT NOT NULL DEFAULT 0,
    "purchase_count" BIGINT NOT NULL DEFAULT 0,
    "updated_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "listing_popularity_pkey" PRIMARY KEY ("listing_id")
);
-- DESC: reading the most popular listings is the only ordered query this table answers.
CREATE INDEX IF NOT EXISTS "listing_popularity_score_idx" ON "listing_popularity" ("score" DESC);
