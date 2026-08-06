-- Backfill for the seller-confirmation step and the reshaped refund machine.
--
-- The canonical schema is edited in place (internal/module/*/migrations), so this exists only for
-- databases that already hold data: it brings an old one to the new shape without dropping rows.
-- A fresh deployment never runs it. Idempotent — every step is guarded — so a half-finished run
-- can simply be repeated.
--
-- Run it as one transaction against the order schema's database:
--   docker compose exec -T db psql -U app -d shopnexus -v ON_ERROR_STOP=1 -1 \
--     -f dev/backfill/2026-08-06-seller-confirmation-and-refund-states.sql

SET search_path = "order", public;

-- 1. The seller-confirmation columns.
ALTER TABLE "order" ADD COLUMN IF NOT EXISTS "confirmed_at" TIMESTAMPTZ;
ALTER TABLE "order" ADD COLUMN IF NOT EXISTS "confirmation_escalated_at" TIMESTAMPTZ;
ALTER TABLE "order" ADD COLUMN IF NOT EXISTS "decline_reason" TEXT;

-- Every order that predates this change was created under the old rule, where the money booked
-- the parcel with no seller step at all. They are therefore confirmed, and stamping them with
-- their own creation time says exactly that. Without this the new sweep would read all of them as
-- sellers who never answered and raise a ticket for each.
UPDATE "order" SET "confirmed_at" = "created_at" WHERE "confirmed_at" IS NULL;

-- 2. The refund lifecycle. 'awaiting-buyer-action' is gone: a seller can no longer refuse a
-- refund, so there is no state where the buyer owes an escalation. The closest true statement
-- about a row sitting there is that nobody has judged it — which is 'disputed'.
--
-- Postgres cannot drop an enum label, so the type is replaced and the column moved across.
-- The CHECK and the partial index both name status literals. They have to go before the type is
-- replaced, or the ALTER re-resolves those literals against the new type and compares them to a
-- column that is still the old one ("operator does not exist: refund_status = refund_status_old").
ALTER TABLE "refund" DROP CONSTRAINT IF EXISTS "refund_deadline_matches_status";
DROP INDEX IF EXISTS "refund_one_active_per_order";

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_enum e
        JOIN pg_type t ON t.oid = e.enumtypid
        WHERE t.typname = 'refund_status' AND e.enumlabel = 'awaiting-buyer-action'
    ) THEN
        -- The deadline goes with the state: 'disputed' waits on a human, and the CHECK below
        -- allows a deadline only where a party is on the clock.
        UPDATE "refund" SET "status" = 'disputed', "deadline_at" = NULL
        WHERE "status" = 'awaiting-buyer-action';

        ALTER TYPE "refund_status" RENAME TO "refund_status_old";
        CREATE TYPE "refund_status" AS ENUM (
            'awaiting-seller-review', 'disputed', 'returning', 'returned',
            'accepted', 'rejected', 'cancelled'
        );
        ALTER TABLE "refund"
            ALTER COLUMN "status" DROP DEFAULT,
            ALTER COLUMN "status" TYPE "refund_status" USING "status"::text::"refund_status",
            ALTER COLUMN "status" SET DEFAULT 'awaiting-seller-review';
        DROP TYPE "refund_status_old";
    END IF;
END $$;

-- 3. rejection_reason is dropped, and the seller's words are not. They are a fact about a decision,
-- so they move to where this schema keeps history rather than being deleted with the column.
-- Guarded on the column still existing, so a second run adds nothing.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'order' AND table_name = 'refund' AND column_name = 'rejection_reason'
    ) THEN
        INSERT INTO "audit_log" (
            "version", "table_name", "record_id", "change_type", "code", "changed_at", "diff", "snapshot"
        )
        SELECT
            COALESCE((
                SELECT max(a."version") + 1 FROM "audit_log" a
                WHERE a."table_name" = 'refund' AND a."record_id" = r."id"
            ), 1),
            'refund', r."id", 'update', 'refund.rejected',
            COALESCE(r."seller_decided_at", r."created_at"),
            jsonb_build_object('rejection_reason', r."rejection_reason"),
            jsonb_build_object('status', r."status"::text)
        FROM "refund" r
        WHERE r."rejection_reason" IS NOT NULL;

        ALTER TABLE "refund" DROP CONSTRAINT IF EXISTS "refund_rejection_needs_decision";
        ALTER TABLE "refund" DROP COLUMN "rejection_reason";
    END IF;
END $$;

-- 4. The constraints and indexes whose predicates named the dropped state, plus the two new order
-- CHECKs. Dropped first so a re-run replaces rather than conflicts.
ALTER TABLE "refund" ADD CONSTRAINT "refund_deadline_matches_status" CHECK (
    ("deadline_at" IS NOT NULL) = ("status" IN ('awaiting-seller-review', 'returned'))
);

CREATE UNIQUE INDEX "refund_one_active_per_order"
    ON "refund" ("order_id")
    WHERE "status" IN ('awaiting-seller-review', 'disputed', 'returning', 'returned');

ALTER TABLE "order" DROP CONSTRAINT IF EXISTS "order_receipt_needs_confirmation";
ALTER TABLE "order" ADD CONSTRAINT "order_receipt_needs_confirmation" CHECK (
    "received_at" IS NULL OR "confirmed_at" IS NOT NULL
);
ALTER TABLE "order" DROP CONSTRAINT IF EXISTS "order_decline_is_a_cancellation";
ALTER TABLE "order" ADD CONSTRAINT "order_decline_is_a_cancellation" CHECK (
    "decline_reason" IS NULL
    OR ("cancelled_at" IS NOT NULL AND "confirmed_at" IS NULL)
);

DROP INDEX IF EXISTS "order_awaiting_confirmation_idx";
CREATE INDEX "order_awaiting_confirmation_idx"
    ON "order" ("created_at")
    WHERE "confirmed_at" IS NULL AND "completed_at" IS NULL AND "cancelled_at" IS NULL
      AND "confirmation_escalated_at" IS NULL;

-- 5. What the run did, so the output is checkable rather than merely silent.
SELECT 'orders backfilled as confirmed' AS what, count(*) AS rows FROM "order" WHERE "confirmed_at" IS NOT NULL
UNION ALL SELECT 'orders still awaiting confirmation', count(*) FROM "order" WHERE "confirmed_at" IS NULL
UNION ALL SELECT 'rejection reasons preserved in audit_log', count(*) FROM "audit_log"
    WHERE "table_name" = 'refund' AND "code" = 'refund.rejected'
UNION ALL SELECT 'refunds now disputed', count(*) FROM "refund" WHERE "status" = 'disputed';
