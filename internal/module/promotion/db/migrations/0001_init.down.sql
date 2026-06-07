-- Drops all promotion schema objects in reverse dependency order.
-- Indexes (including constraint-backed unique indexes) drop with their tables.

-- Tables (child tables first)
DROP TABLE IF EXISTS "promotion"."schedule";
DROP TABLE IF EXISTS "promotion"."ref";
DROP TABLE IF EXISTS "promotion"."promotion";

-- Enums
DROP TYPE IF EXISTS "promotion"."ref_type";
DROP TYPE IF EXISTS "promotion"."type";

DROP SCHEMA IF EXISTS "promotion";
