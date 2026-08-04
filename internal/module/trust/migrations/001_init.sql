-- Module: trust — canonical schema
-- =============================================
-- Module: Trust & Safety
-- Schema: trust
-- Description: Two-way transaction feedback, product reviews with their replies
--              and helpfulness votes, per-account reputation aggregates
--              (as-seller / as-buyer), and the ticket queue — abuse reports, refund
--              admin resolution workflow. Every rating in this module is on the
--              same 1..5 scale. Cross-module refs (account_id, order_id, listing_id,
--              reported ref_id) carry no FK.
-- =============================================

-- Enums
CREATE TYPE "feedback_direction" AS ENUM ('buyer-to-seller', 'seller-to-buyer');
CREATE TYPE "reputation_role" AS ENUM ('seller', 'buyer');
-- What a ticket is about. One list, because every one of these is the same shape: somebody
-- submitted something and somebody — a moderator, or the platform itself — answers in a thread.
-- The `report-*` kinds are the abuse reports this table replaced.
CREATE TYPE "ticket_kind" AS ENUM (
    'report-listing', 'report-account', 'report-message', 'report-review', 'report-review-reply',
    'refund-dispute', 'order-issue', 'payment', 'account', 'feature-request', 'other'
);
-- The thing a ticket is about, when it is about something. NULL for a feature request.
CREATE TYPE "ticket_ref_type" AS ENUM (
    'listing', 'account', 'message', 'review', 'review-reply', 'order', 'refund'
);
-- Only the report kinds carry one: what the reporter says is wrong.
CREATE TYPE "ticket_reason" AS ENUM ('scam', 'counterfeit', 'prohibited', 'harassment', 'spam', 'inappropriate', 'other');
-- `reviewing` is claimed by a moderator. There is no separate `actioned`/`dismissed`: a resolved
-- ticket's "action_taken" already says which, and two enums for one fact drift.
CREATE TYPE "ticket_status" AS ENUM ('open', 'reviewing', 'resolved');
CREATE TYPE "ticket_action" AS ENUM ('none', 'listing-removed', 'message-removed', 'account-suspended', 'warning', 'refund-granted', 'refund-refused');

-- Tables


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
-- The ratee's published history, newest first. The id trails the timestamp because the
-- cursor compares the pair: rows written in one transaction share "created_at" exactly.
CREATE INDEX IF NOT EXISTS "feedback_ratee_id_idx" ON "feedback" ("ratee_id", "created_at" DESC, "id" DESC) WHERE "published_at" IS NOT NULL;
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
    -- Whose reputation this rating counts towards, frozen from the order at submission.
    -- Asking catalog on every edit made the aggregate depend on a listing still being
    -- readable: a listing back in "pending" answers 404 to its own buyer, and the reply
    -- was an account id of 0.
    "seller_id" BIGINT NOT NULL, -- cross-ref account.account; no FK
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
    -- NULL until the author edits it. A review rewritten after the seller answered it
    -- should say so, and the reply thread cannot say it on its own.
    "updated_at" TIMESTAMPTZ,

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
CREATE INDEX IF NOT EXISTS "review_listing_id_idx" ON "review" ("listing_id", "created_at" DESC, "id" DESC);
-- The same page sorted by helpfulness, which is why the tally is a column.
-- The tuple the cursor compares, so paging by helpfulness is still an index scan.
CREATE INDEX IF NOT EXISTS "review_listing_id_helpful_idx" ON "review" ("listing_id", "helpful_count" DESC, "id" DESC);

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

-- Which orders have already been folded into the two order counters above. The settled
-- event arrives over an at-least-once bus, so a redelivery would bump a counter twice;
-- this key is written in the same transaction as the bump, which makes the second
-- attempt a no-op instead of a second effect.
CREATE TABLE IF NOT EXISTS "order_outcome" (
    "order_id" BIGINT NOT NULL, -- cross-ref order.order; no FK
    "completed" BOOLEAN NOT NULL,
    "recorded_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "order_outcome_pkey" PRIMARY KEY ("order_id")
);

-- A ticket: one thing a user raised, and the conversation about it.
--
-- It replaced the abuse-report table and the order module's refund_dispute, because all three were
-- the same row with a different name: a requester, what it is about, a moderator who takes it, and
-- a verdict. Keeping them apart meant a user had three places to look and this platform had three
-- queues to staff.
--
-- The discussion itself is not here. `conversation_id` points at chat's thread, whose first message
-- is what the requester wrote and whose attachments are the photos they sent — so a ticket needs no
-- body column, no attachment array and no second upload path. Nullable because the thread is
-- written in another schema: the row lands first and the thread is created right after, or repaired
-- on the next read.
CREATE TABLE IF NOT EXISTS "ticket" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "requester_id" BIGINT NOT NULL, -- cross-ref account.account; no FK
    "kind" "ticket_kind" NOT NULL,
    "subject" TEXT NOT NULL,
    -- The polymorphic target, kinded by "ref_type". Both NULL together: a feature request is about
    -- nothing in particular.
    "ref_type" "ticket_ref_type",
    "ref_id" BIGINT,
    "reason" "ticket_reason", -- report kinds only
    "status" "ticket_status" NOT NULL DEFAULT 'open',
    -- The moderator who claimed it. Never published to the requester: support answers as the desk,
    -- so a decision is the platform's and not a named person's to be argued with afterwards.
    "assignee_id" BIGINT,
    "conversation_id" BIGINT, -- cross-ref chat.conversation; no FK

    -- The verdict. "action_taken" = 'none' is a ticket looked at and turned down, which is what the
    -- old report table spelled as its own status.
    "action_taken" "ticket_action",
    "resolved_by_id" BIGINT,
    "resolved_at" TIMESTAMPTZ,
    "resolution_note" TEXT,
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "ticket_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "ticket_subject_present" CHECK (length(btrim("subject")) > 0),
    -- A target is a type and an id or neither.
    CONSTRAINT "ticket_ref_together" CHECK (("ref_type" IS NULL) = ("ref_id" IS NULL)),
    -- Being resolved and having a verdict are the same fact, and a verdict has an author.
    CONSTRAINT "ticket_resolution_together" CHECK (
        (("status" = 'resolved') = ("resolved_at" IS NOT NULL))
        AND (("resolved_at" IS NULL) = ("resolved_by_id" IS NULL))
        AND (("resolved_at" IS NULL) = ("action_taken" IS NULL))
    ),
    -- Claimed is a state with an owner.
    CONSTRAINT "ticket_assignee_when_reviewing" CHECK (
        "status" <> 'reviewing' OR "assignee_id" IS NOT NULL
    ),
    CONSTRAINT "ticket_conversation_id_key" UNIQUE ("conversation_id")
);
CREATE INDEX IF NOT EXISTS "ticket_ref_idx" ON "ticket" ("ref_type", "ref_id");
-- "My tickets", newest first: the one list a user checks for everything they raised.
CREATE INDEX IF NOT EXISTS "ticket_requester_idx" ON "ticket" ("requester_id", "created_at" DESC, "id" DESC);
-- The moderator queue: what nobody has resolved, oldest first, which is the order it is worked.
CREATE INDEX IF NOT EXISTS "ticket_queue_idx"
    ON "ticket" ("created_at", "id")
    WHERE "status" IN ('open', 'reviewing');
-- One open ticket per requester per target, which is the rule the report table held: a second
-- complaint about the same listing is the same complaint.
CREATE UNIQUE INDEX IF NOT EXISTS "ticket_one_open_per_target"
    ON "ticket" ("requester_id", "ref_type", "ref_id")
    WHERE "status" IN ('open', 'reviewing') AND "ref_type" IS NOT NULL;
