DROP TRIGGER IF EXISTS "comment_cached_rating" ON "catalog"."comment";
DROP TRIGGER IF EXISTS "sku_cached_price" ON "catalog"."product_sku";

DROP FUNCTION IF EXISTS "catalog"."trg_comment_cached_rating"();
DROP FUNCTION IF EXISTS "catalog"."trg_sku_cached_price"();
DROP FUNCTION IF EXISTS "catalog"."refresh_spu_cached_rating"(UUID);
DROP FUNCTION IF EXISTS "catalog"."refresh_spu_cached_price"(UUID);

DROP INDEX IF EXISTS "catalog"."product_spu_cached_rating_idx";
DROP INDEX IF EXISTS "catalog"."product_spu_cached_price_idx";
DROP INDEX IF EXISTS "catalog"."comment_ref_type_ref_id_idx";

ALTER TABLE "catalog"."product_spu"
    DROP COLUMN "cached_rating",
    DROP COLUMN "cached_price";
