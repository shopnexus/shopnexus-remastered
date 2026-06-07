-- Drops all account schema objects in reverse dependency order.
-- Indexes are dropped before tables; types after tables; schema last.

-- Indexes
DROP INDEX IF EXISTS "account"."favorite_spu_id_idx";
DROP INDEX IF EXISTS "account"."contact_account_id_idx";
DROP INDEX IF EXISTS "account"."notification_date_created_idx";
DROP INDEX IF EXISTS "account"."notification_channel_idx";
DROP INDEX IF EXISTS "account"."notification_type_idx";
DROP INDEX IF EXISTS "account"."notification_account_id_idx";

-- Tables (dependent tables first; profile FKs contact via default_contact_id)
DROP TABLE IF EXISTS "account"."favorite";
DROP TABLE IF EXISTS "account"."notification";
DROP TABLE IF EXISTS "account"."profile";
DROP TABLE IF EXISTS "account"."contact";
DROP TABLE IF EXISTS "account"."account";

-- Enums
DROP TYPE IF EXISTS "account"."role";
DROP TYPE IF EXISTS "account"."address_type";
DROP TYPE IF EXISTS "account"."gender";
DROP TYPE IF EXISTS "account"."status";

DROP SCHEMA IF EXISTS "account";
