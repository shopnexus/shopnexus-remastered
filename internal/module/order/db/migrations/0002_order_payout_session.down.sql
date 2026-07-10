ALTER TABLE "order"."order" DROP CONSTRAINT IF EXISTS "order_payout_session_id_fkey";
ALTER TABLE "order"."order" DROP COLUMN IF EXISTS "payout_session_id";
