-- Drops all catalog schema objects in reverse dependency order.
-- The circular product_spu <-> product_sku FK is removed first so the
-- tables can drop cleanly; indexes drop with their tables.

ALTER TABLE IF EXISTS "catalog"."product_spu"
    DROP CONSTRAINT IF EXISTS "product_featured_sku_id_fkey";

-- Tables (dependent tables first)
DROP TABLE IF EXISTS "catalog"."account_interest";
DROP TABLE IF EXISTS "catalog"."tag_embedding";
DROP TABLE IF EXISTS "catalog"."category_embedding";
DROP TABLE IF EXISTS "catalog"."product_embedding";
DROP TABLE IF EXISTS "catalog"."search_sync";
DROP TABLE IF EXISTS "catalog"."comment";
DROP TABLE IF EXISTS "catalog"."product_spu_tag";
DROP TABLE IF EXISTS "catalog"."tag";
DROP TABLE IF EXISTS "catalog"."product_sku";
DROP TABLE IF EXISTS "catalog"."product_spu";
DROP TABLE IF EXISTS "catalog"."category";

-- Enums
DROP TYPE IF EXISTS "catalog"."comment_ref_type";
DROP TYPE IF EXISTS "catalog"."search_sync_ref_type";

DROP SCHEMA IF EXISTS "catalog";
