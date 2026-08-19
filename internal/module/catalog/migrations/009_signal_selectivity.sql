-- How common each thing a search signal can name actually is, so a compiled predicate's weight
-- can be scaled by how much its value narrows the catalogue rather than only by which attribute
-- it is. "condition" = 'new' matches two thirds of this marketplace and 'damaged' a twentieth,
-- and a per-attribute weight moves a page by the same amount for both.
--
-- One table over three kinds rather than a cached column per source: a count belongs to
-- "category", to "tag" and to "condition" alike, and "condition" has no row of its own anywhere
-- to hang a counter on.
--
-- "kind" holds the predicate kinds the compiled signal already uses (catalog/port), and "key" is
-- whatever identifies the value within that kind: the category id as text, the tag slug, the
-- condition label. Text for all three because that is the one type all three share, and nothing
-- joins this table — it is read whole, a few dozen rows, and looked up in memory.
--
-- Refreshed by a sweep, never by a listing write: a count a pass behind moves a weight in the
-- third decimal, which is not worth a second write on every publish.
CREATE TABLE IF NOT EXISTS "signal_selectivity" (
    "kind" VARCHAR(20) NOT NULL,
    "key" VARCHAR(120) NOT NULL,
    "listings" BIGINT NOT NULL,
    "updated_at" TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT "signal_selectivity_pkey" PRIMARY KEY ("kind", "key")
);
