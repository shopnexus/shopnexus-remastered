-- Backfill for refunds whose evidence carries the same resource more than once.
--
-- `AddAttachments` used to append whatever it was handed, so a resource submitted again
-- while the case was open was stored a second time. The domain deduplicates now, but only
-- on write: a row already holding a duplicate keeps it, and the case then reads as
-- carrying more photos than it does — which matters, because these arrays are the record a
-- verdict gets reached on.
--
-- This exists only for databases that already hold data; a fresh deployment never runs it.
-- Idempotent — the guard compares against the deduplicated array, so a second run updates
-- nothing.
--
-- Run it as one transaction against the order schema's database:
--   docker compose exec -T db psql -U app -d shopnexus -v ON_ERROR_STOP=1 -1 \
--     -f dev/backfill/2026-08-19-refund-evidence-deduplicated.sql

SET search_path = "order", public;

-- First occurrence wins and submission order is kept: the array is the order the buyer
-- built their case in, and a verdict reads it that way. DISTINCT ON takes the lowest
-- ordinality per resource; the outer array_agg puts them back in that order.
UPDATE "refund" r
SET "attachments" = d."deduplicated"
FROM (
    SELECT r2."id", array_agg(first."key" ORDER BY first."ord") AS "deduplicated"
    FROM "refund" r2
    CROSS JOIN LATERAL (
        SELECT DISTINCT ON (u."key") u."key", u."ord"
        FROM unnest(r2."attachments") WITH ORDINALITY AS u("key", "ord")
        ORDER BY u."key", u."ord"
    ) first
    GROUP BY r2."id"
) d
WHERE r."id" = d."id"
  AND r."attachments" <> d."deduplicated";

-- What the run did, so the output is checkable rather than merely silent. The first number
-- is the one that has to be zero.
SELECT 'refunds still carrying a duplicate' AS what, count(*) AS rows
FROM "refund"
WHERE cardinality("attachments") <> (
    SELECT count(DISTINCT "key") FROM unnest("attachments") AS "key"
)
UNION ALL SELECT 'refunds with evidence', count(*) FROM "refund"
    WHERE cardinality("attachments") > 0
UNION ALL SELECT 'pieces of evidence on record', COALESCE(sum(cardinality("attachments")), 0)
    FROM "refund";
