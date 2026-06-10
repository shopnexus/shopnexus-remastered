-- Denormalized sort keys on product_spu so the list runtime (repolist) can
-- keyset-sort price and rating as real NOT NULL columns. Maintained by triggers
-- off product_sku / comment, backfilled at the end.

ALTER TABLE "catalog"."product_spu"
    ADD COLUMN "cached_price" BIGINT NOT NULL DEFAULT 0, -- Cheapest live SKU price; 0 when the SPU has none
    ADD COLUMN "cached_rating" DOUBLE PRECISION NOT NULL DEFAULT 0; -- Mean review score; 0 when no reviews

-- Backs the per-SPU rating aggregate the trigger runs (and ListRating).
CREATE INDEX IF NOT EXISTS "comment_ref_type_ref_id_idx" ON "catalog"."comment" ("ref_type", "ref_id");
CREATE INDEX IF NOT EXISTS "product_spu_cached_price_idx" ON "catalog"."product_spu" ("cached_price");
CREATE INDEX IF NOT EXISTS "product_spu_cached_rating_idx" ON "catalog"."product_spu" ("cached_rating");

-- cached_price = cheapest live SKU of one SPU (0 when it has none).
CREATE OR REPLACE FUNCTION "catalog"."refresh_spu_cached_price"(p_spu_id UUID)
RETURNS VOID
LANGUAGE sql AS $$
    UPDATE "catalog"."product_spu"
    SET "cached_price" = COALESCE((
        SELECT MIN("price") FROM "catalog"."product_sku"
        WHERE "spu_id" = p_spu_id AND "date_deleted" IS NULL
    ), 0)
    WHERE "id" = p_spu_id;
$$;

CREATE OR REPLACE FUNCTION "catalog"."trg_sku_cached_price"()
RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM "catalog"."refresh_spu_cached_price"(OLD."spu_id");
        RETURN OLD;
    END IF;
    PERFORM "catalog"."refresh_spu_cached_price"(NEW."spu_id");
    IF TG_OP = 'UPDATE' AND NEW."spu_id" <> OLD."spu_id" THEN
        PERFORM "catalog"."refresh_spu_cached_price"(OLD."spu_id");
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER "sku_cached_price"
AFTER INSERT OR UPDATE OR DELETE ON "catalog"."product_sku"
FOR EACH ROW EXECUTE FUNCTION "catalog"."trg_sku_cached_price"();

-- cached_rating = mean score of an SPU's reviews (0 when none).
CREATE OR REPLACE FUNCTION "catalog"."refresh_spu_cached_rating"(p_spu_id UUID)
RETURNS VOID
LANGUAGE sql AS $$
    UPDATE "catalog"."product_spu"
    SET "cached_rating" = COALESCE((
        SELECT AVG("score") FROM "catalog"."comment"
        WHERE "ref_type" = 'ProductSpu' AND "ref_id" = p_spu_id
    ), 0)
    WHERE "id" = p_spu_id;
$$;

CREATE OR REPLACE FUNCTION "catalog"."trg_comment_cached_rating"()
RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF OLD."ref_type" = 'ProductSpu' THEN
            PERFORM "catalog"."refresh_spu_cached_rating"(OLD."ref_id");
        END IF;
        RETURN OLD;
    END IF;
    IF NEW."ref_type" = 'ProductSpu' THEN
        PERFORM "catalog"."refresh_spu_cached_rating"(NEW."ref_id");
    END IF;
    IF TG_OP = 'UPDATE' AND OLD."ref_type" = 'ProductSpu' AND OLD."ref_id" <> NEW."ref_id" THEN
        PERFORM "catalog"."refresh_spu_cached_rating"(OLD."ref_id");
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER "comment_cached_rating"
AFTER INSERT OR UPDATE OR DELETE ON "catalog"."comment"
FOR EACH ROW EXECUTE FUNCTION "catalog"."trg_comment_cached_rating"();

UPDATE "catalog"."product_spu" spu SET
    "cached_price" = COALESCE((
        SELECT MIN(sk."price") FROM "catalog"."product_sku" sk
        WHERE sk."spu_id" = spu."id" AND sk."date_deleted" IS NULL
    ), 0),
    "cached_rating" = COALESCE((
        SELECT AVG(c."score") FROM "catalog"."comment" c
        WHERE c."ref_type" = 'ProductSpu' AND c."ref_id" = spu."id"
    ), 0);
