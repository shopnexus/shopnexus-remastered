-- Where the goods are. A C2C buyer filters by province before anything else — collecting in
-- person, or just trusting a seller two districts away over one across the country — and
-- "listings near me" is the browse feed's most-used sort.
--
-- Denormalized from the seller's pickup contact, the same reason "cached_rating" is: the address
-- lives in account's schema and cannot be joined. Snapshotted when the listing is published, so a
-- live listing always has one and the filter covers every row a buyer can see. A draft has none.
CREATE EXTENSION IF NOT EXISTS postgis;

ALTER TABLE "listing"
    -- Administrative levels, mirroring account.contact: "district_code" is nullable because
    -- Vietnam has no district tier (province -> ward) and other countries do. The names are
    -- display snapshots, so a later territorial rename does not rewrite what a card shows.
    ADD COLUMN IF NOT EXISTS "province_code" VARCHAR(20),
    ADD COLUMN IF NOT EXISTS "province_name" VARCHAR(100),
    ADD COLUMN IF NOT EXISTS "district_code" VARCHAR(20),
    ADD COLUMN IF NOT EXISTS "district_name" VARCHAR(100),
    ADD COLUMN IF NOT EXISTS "ward_code" VARCHAR(20),
    ADD COLUMN IF NOT EXISTS "ward_name" VARCHAR(100),
    -- NULL when the seller's address was never geocoded. Such a listing still filters by
    -- province; it just cannot answer "within 5 km", which is why distance is a sort a buyer
    -- opts into rather than the default.
    ADD COLUMN IF NOT EXISTS "location" geography (Point, 4326);

ALTER TABLE "listing"
    DROP CONSTRAINT IF EXISTS "listing_district_code_name_together";

ALTER TABLE "listing"
    ADD CONSTRAINT "listing_district_code_name_together" CHECK (
        ("district_code" IS NULL) = ("district_name" IS NULL)
    );

-- The browse feed's own filter: province, then district within it. Partial, because a buyer only
-- ever narrows what is on sale — a draft or a deleted row is never in this list.
CREATE INDEX IF NOT EXISTS "listing_area_idx"
    ON "listing" ("province_code", "district_code")
    WHERE "status" = 'active' AND "deleted_at" IS NULL;

-- "Near me" is ST_DWithin against this. Partial for the same reason, and GIST because a
-- geography needs one: a btree cannot answer a radius.
CREATE INDEX IF NOT EXISTS "listing_location_gist"
    ON "listing" USING gist ("location")
    WHERE "status" = 'active' AND "deleted_at" IS NULL;
