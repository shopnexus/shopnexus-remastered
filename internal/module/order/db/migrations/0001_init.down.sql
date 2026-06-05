-- Drops all order schema objects in reverse dependency order.

DROP FUNCTION IF EXISTS "order".is_cancelled("order"."status", "order"."status", "order"."status");

-- Tables (most-dependent first)
DROP TABLE IF EXISTS "order"."refund_dispute";
DROP TABLE IF EXISTS "order"."refund";
DROP TABLE IF EXISTS "order"."item";
DROP TABLE IF EXISTS "order"."order";
DROP TABLE IF EXISTS "order"."transport";
DROP VIEW IF EXISTS "order"."transaction_settled";
DROP TABLE IF EXISTS "order"."transaction";
DROP TABLE IF EXISTS "order"."payment_session";
DROP TABLE IF EXISTS "order"."cart_item";

-- Enums
DROP TYPE IF EXISTS "order"."dispute_status";
DROP TYPE IF EXISTS "order"."refund_status";
DROP TYPE IF EXISTS "order"."status";

DROP SCHEMA IF EXISTS "order";
