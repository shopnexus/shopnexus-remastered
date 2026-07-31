-- Module: trust — canonical schema
-- =============================================
-- Module: Trust & Safety
-- Schema: trust
-- Description: Two-way transaction feedback, product reviews with their replies
--              and helpfulness votes, per-account reputation aggregates
--              (as-seller / as-buyer), and polymorphic abuse reports with an
--              admin resolution workflow. Every rating in this module is on the
--              same 1..5 scale. Cross-module refs (account_id, order_id, listing_id,
--              reported ref_id) carry no FK.
-- =============================================

-- Enums
CREATE TYPE "feedback_direction" AS ENUM ('buyer-to-seller', 'seller-to-buyer');
CREATE TYPE "reputation_role" AS ENUM ('seller', 'buyer');
CREATE TYPE "report_ref_type" AS ENUM ('listing', 'account', 'message', 'review', 'review-reply');
CREATE TYPE "report_reason" AS ENUM ('scam', 'counterfeit', 'prohibited', 'harassment', 'spam', 'inappropriate', 'other');
CREATE TYPE "report_status" AS ENUM ('open', 'reviewing', 'actioned', 'dismissed');
CREATE TYPE "report_action" AS ENUM ('none', 'listing-removed', 'message-removed', 'account-suspended', 'warning');

-- Tables

CREATE TABLE IF NOT EXISTS "audit_log" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "version" BIGINT NOT NULL DEFAULT 1, -- Incremented on each change to the same record
    "table_name" VARCHAR(100) NOT NULL,
    "record_id" BIGINT NOT NULL,
    "change_type" VARCHAR(10) NOT NULL, -- 'insert', 'update', 'delete'
    "code" VARCHAR(100) NOT NULL, -- e.g. Business code 'review.delete', 'report.resolve'
    "changed_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "changed_by" BIGINT, -- account_id of the user who made the change (if applicable)
    "diff" JSONB NOT NULL, -- JSON diff of the record's fields (for insert only, other diff = snapshot)
    "snapshot" JSONB NOT NULL, -- Full record values after the change
    CONSTRAINT "audit_log_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "audit_log_table_name_record_id_version_key" UNIQUE ("table_name", "record_id", "version")
);

-- One rating in one direction for one completed order; blind until revealed.
CREATE TABLE IF NOT EXISTS "feedback" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "order_id" BIGINT NOT NULL, -- cross-ref order.order; no FK
    "rater_id" BIGINT NOT NULL, -- account giving the rating
    "ratee_id" BIGINT NOT NULL, -- account receiving the rating
    "direction" "feedback_direction" NOT NULL,
    "rating" SMALLINT NOT NULL,
    "comment" TEXT NOT NULL DEFAULT '',
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "published_at" TIMESTAMPTZ, -- NULL = still blind; only published rows are visible and counted

    CONSTRAINT "feedback_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "feedback_order_direction_key" UNIQUE ("order_id", "direction"),
    CONSTRAINT "feedback_rating_range_chk" CHECK ("rating" BETWEEN 1 AND 5)
);
CREATE INDEX IF NOT EXISTS "feedback_ratee_id_idx" ON "feedback" ("ratee_id", "created_at" DESC) WHERE "published_at" IS NOT NULL;
CREATE INDEX IF NOT EXISTS "feedback_rater_id_idx" ON "feedback" ("rater_id");
-- The reveal job: blind rows whose window has run out. The partial index above only
-- covers what is already published, so without this the job scans the whole table
-- looking for the handful of rows it has to act on.
CREATE INDEX IF NOT EXISTS "feedback_unpublished_idx"
    ON "feedback" ("created_at")
    WHERE "published_at" IS NULL;

-- A buyer's review of a product they bought. "order_id" is NOT NULL by design: no
-- purchase, no review. Ratings here use the same 1..5 scale as "feedback".
CREATE TABLE IF NOT EXISTS "review" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "listing_id" BIGINT NOT NULL, -- cross-ref catalog.listing; no FK
    "order_id" BIGINT NOT NULL, -- cross-ref order.order; no FK
    "author_id" BIGINT NOT NULL, -- cross-ref account.account; no FK
    "rating" SMALLINT NOT NULL,
    "body" TEXT NOT NULL DEFAULT '',
    -- Photos of the item as received; resource ids owned by the common module, held
    -- inline for the same reason catalog.listing and chat.message do it — a review
    -- and its images sit in two schemas, and writing both atomically stops being
    -- possible once the modules are split apart.
    "attachments" BIGINT[] NOT NULL DEFAULT '{}',
    -- Vote tallies, denormalized from "review_vote" for the same reason
    -- catalog.listing caches its rating: sorting a product's reviews by
    -- helpfulness has to be an ordered index scan, and an aggregate computed per
    -- query is neither indexable nor seekable by a cursor.
    "helpful_count" BIGINT NOT NULL DEFAULT 0,
    "not_helpful_count" BIGINT NOT NULL DEFAULT 0,
    "reply_count" BIGINT NOT NULL DEFAULT 0, -- so a page need not count replies per row
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "review_pkey" PRIMARY KEY ("id"),
    -- One review per order, so buying the same product twice earns two reviews.
    CONSTRAINT "review_spu_author_order_key" UNIQUE ("listing_id", "author_id", "order_id"),
    CONSTRAINT "review_rating_range_chk" CHECK ("rating" BETWEEN 1 AND 5),
    -- A tally that has gone negative means the maintaining code is wrong, and a
    -- product page showing -3 helpful is worse than the write failing.
    CONSTRAINT "review_counts_non_negative_chk" CHECK (
        "helpful_count" >= 0 AND "not_helpful_count" >= 0 AND "reply_count" >= 0
    )
);
-- The product page: one SPU's reviews, newest first. Also the source catalog reads to
-- recompute its cached_rating.
CREATE INDEX IF NOT EXISTS "review_listing_id_idx" ON "review" ("listing_id", "created_at" DESC);
-- The same page sorted by helpfulness, which is why the tally is a column.
CREATE INDEX IF NOT EXISTS "review_listing_id_helpful_idx" ON "review" ("listing_id", "helpful_count" DESC);
-- "my reviews".
CREATE INDEX IF NOT EXISTS "review_author_id_idx" ON "review" ("author_id", "created_at" DESC);

-- Flat replies under a review: a seller answering, a buyer following up. No rating
-- and no order — a reply is not a review.
CREATE TABLE IF NOT EXISTS "review_reply" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "review_id" BIGINT NOT NULL,
    "author_id" BIGINT NOT NULL, -- cross-ref account.account; no FK
    "body" TEXT NOT NULL,
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "review_reply_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "review_reply_review_id_fkey" FOREIGN KEY ("review_id")
        REFERENCES "review" ("id") ON DELETE CASCADE
);
-- A review's thread, oldest first. Not unique per author: replies are unlimited.
CREATE INDEX IF NOT EXISTS "review_reply_review_id_idx" ON "review_reply" ("review_id", "created_at");

-- Helpfulness votes on a review. The pair is the whole row, so it is the key.
CREATE TABLE IF NOT EXISTS "review_vote" (
    "review_id" BIGINT NOT NULL,
    "account_id" BIGINT NOT NULL, -- cross-ref account.account; no FK
    "vote" SMALLINT NOT NULL, -- -1 = downvote, 1 = upvote
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "review_vote_pkey" PRIMARY KEY ("review_id", "account_id"),
    -- No neutral value: withdrawing a vote deletes the row. A stored zero is a row
    -- that says nothing, and it would also have to be excluded from every tally.
    CONSTRAINT "review_vote_value" CHECK ("vote" IN (-1, 1)),
    CONSTRAINT "review_vote_review_id_fkey" FOREIGN KEY ("review_id")
        REFERENCES "review" ("id") ON DELETE CASCADE
);

-- Per-account, per-role reputation aggregate. avg = rating_sum / rating_count, computed on read.
CREATE TABLE IF NOT EXISTS "reputation" (
    "account_id" BIGINT NOT NULL,
    "role" "reputation_role" NOT NULL,
    -- From "feedback": how the counterparty found the transaction. Both roles.
    "rating_sum" BIGINT NOT NULL DEFAULT 0,
    "rating_count" BIGINT NOT NULL DEFAULT 0,
    -- From "review": how buyers found the goods. Kept apart rather than added to the
    -- pair above, because one order can produce both and summing double-counts it —
    -- and "ships on time" is not the same claim as "the item was as described".
    -- Sellers only: nobody reviews a buyer's products.
    "review_rating_sum" BIGINT NOT NULL DEFAULT 0,
    "review_rating_count" BIGINT NOT NULL DEFAULT 0,
    "completed_orders" BIGINT NOT NULL DEFAULT 0,
    "cancelled_orders" BIGINT NOT NULL DEFAULT 0,
    -- Last recompute of this aggregate, not an audit field: "audit_log" has the
    -- change trail, this says whether the numbers are stale.
    "updated_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "reputation_pkey" PRIMARY KEY ("account_id", "role"),
    CONSTRAINT "reputation_reviews_are_seller_only" CHECK (
        "role" = 'seller'
        OR ("review_rating_sum" = 0 AND "review_rating_count" = 0)
    ),
    -- Counters only ever go up; a negative one means the recompute is broken.
    CONSTRAINT "reputation_counters_non_negative" CHECK (
        "rating_sum" >= 0 AND "rating_count" >= 0
        AND "review_rating_sum" >= 0 AND "review_rating_count" >= 0
        AND "completed_orders" >= 0 AND "cancelled_orders" >= 0
    )
);

-- Polymorphic abuse report with admin resolution.
CREATE TABLE IF NOT EXISTS "report" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "reporter_id" BIGINT NOT NULL,
    "ref_type" "report_ref_type" NOT NULL,
    "ref_id" BIGINT NOT NULL, -- polymorphic target, kinded by "ref_type"
    "reason" "report_reason" NOT NULL,
    "detail" TEXT NOT NULL DEFAULT '',
    "status" "report_status" NOT NULL DEFAULT 'open',
    "action_taken" "report_action", -- NULL until resolved
    "resolved_by_id" BIGINT, -- admin account
    "resolved_at" TIMESTAMPTZ,
    "resolution_note" TEXT,
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "report_pkey" PRIMARY KEY ("id")
);
CREATE INDEX IF NOT EXISTS "report_ref_type_ref_id_idx" ON "report" ("ref_type", "ref_id");
CREATE INDEX IF NOT EXISTS "report_reporter_id_idx" ON "report" ("reporter_id", "created_at" DESC);
-- The moderator queue: unresolved reports, oldest first, which is the order they are
-- worked. Partial on the small hot slice, so a resolved backlog of any size costs
-- nothing — "status" alone has too few values to lead an index, and on its own it could
-- not deliver the ordering either.
CREATE INDEX IF NOT EXISTS "report_queue_idx"
    ON "report" ("created_at")
    WHERE "status" IN ('open', 'reviewing');
CREATE UNIQUE INDEX IF NOT EXISTS "report_one_open_per_target" ON "report" ("reporter_id", "ref_type", "ref_id") WHERE "status" IN ('open', 'reviewing');
