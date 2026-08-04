-- Module: chat — canonical schema
-- Description: Real-time messaging between accounts. Each conversation is a 1-1
--              thread, one per pair of accounts regardless of who buys or sells.
--              Messages carry text, attachments and backend-generated events.

-- "message" is a hypertable, so the extension has to exist before it is created.
-- chat migrates before observability, which cannot be relied on to have added it.
CREATE EXTENSION IF NOT EXISTS timescaledb;

-- Enums
-- Who produced the message. 'system' rows come from the backend (an offer was
-- accepted, an order shipped) and belong to no participant.
--
-- There is no per-message delivery status. Read state is two timestamps on
-- "conversation" instead: see the comment there.
CREATE TYPE "conversation_kind" AS ENUM ('direct', 'ticket');
CREATE TYPE "message_type" AS ENUM ('user', 'system');

-- Tables


-- One thread per pair of accounts, whoever is buying. The pair is stored ordered so the
-- unique constraint cannot be sidestepped by swapping sides, and that CHECK also rules
-- out a thread with oneself. Not listing-scoped: products are referenced per message.
CREATE TABLE IF NOT EXISTS "conversation" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    -- What this thread is. `direct` is two accounts talking, one thread per pair. `ticket` is a
    -- support thread: side B is the support desk's own account rather than a person, so the
    -- moderator who answers stays anonymous and the next one inherits the same thread.
    "kind" "conversation_kind" NOT NULL DEFAULT 'direct',
    -- The ticket this thread belongs to (trust.ticket; no FK, another schema). NULL on a direct
    -- thread, and what makes creating a ticket's thread idempotent: a retry finds the same row.
    "ticket_id" BIGINT,
    "account_a_id" BIGINT NOT NULL, -- cross-ref account.account; no FK
    "account_b_id" BIGINT NOT NULL, -- cross-ref account.account; no FK
    -- Denormalized for inbox ordering, maintained by the service. Starts at
    -- "created_at" so an empty thread still sorts predictably.
    "last_message_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    -- How far each side has read, rather than a status per message row.
    --
    -- "message" is a hypertable, and a per-message flag makes every unread question the
    -- wrong shape against one: the badge for a thread becomes a count with no time
    -- bound, so it cannot exclude a chunk, the inbox needs one such count per row on the
    -- page, and marking a thread read UPDATEs every unread row in it — dirtying old
    -- chunks to record something about now.
    --
    -- Two timestamps answer all three from this row alone. Unread in a thread is
    -- "counterparty's messages after my mark", which is a bounded range on
    -- message_conversation_id_created_at_idx; marking read is one UPDATE here; and the
    -- inbox already has both marks in the row it is scanning. Read receipts fall out of
    -- it too — a message is seen when the other side's mark is at or past its
    -- "created_at" — which is the only delivery state a 1-1 thread needs.
    "account_a_read_at" TIMESTAMPTZ,
    "account_b_read_at" TIMESTAMPTZ,
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "conversation_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "conversation_pair_ordered" CHECK ("account_a_id" < "account_b_id"),
    -- A ticket thread has a ticket and a direct one does not.
    CONSTRAINT "conversation_ticket_matches_kind" CHECK (("kind" = 'ticket') = ("ticket_id" IS NOT NULL))
);
-- One thread per pair — but only for direct threads: a user raises many tickets, and every one of
-- them pairs the same two accounts (them and the desk). Partial, so the rule that matters for a
-- 1-1 chat still cannot be sidestepped by swapping sides.
CREATE UNIQUE INDEX IF NOT EXISTS "conversation_pair_key"
    ON "conversation" ("account_a_id", "account_b_id")
    WHERE "kind" = 'direct';
-- One thread per ticket, which is what makes creating it a retry rather than a duplicate.
CREATE UNIQUE INDEX IF NOT EXISTS "conversation_ticket_id_key"
    ON "conversation" ("ticket_id")
    WHERE "ticket_id" IS NOT NULL;
-- The inbox: "my threads, latest activity first". Two indexes because a participant
-- sits on either side; run the query as a UNION ALL of the two branches so each side
-- stays an ordered index scan instead of collapsing into a sort.
CREATE INDEX IF NOT EXISTS "conversation_account_a_id_idx"
    ON "conversation" ("account_a_id", "last_message_at" DESC);
CREATE INDEX IF NOT EXISTS "conversation_account_b_id_idx"
    ON "conversation" ("account_b_id", "last_message_at" DESC);

-- Individual message within a conversation.
CREATE TABLE IF NOT EXISTS "message" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "conversation_id" BIGINT NOT NULL,
    "sender_id" BIGINT, -- the account that sent it; NULL on 'system' rows
    "type" "message_type" NOT NULL DEFAULT 'user',
    "body" TEXT NOT NULL,
    -- Resource ids owned by the common module, held inline rather than through
    -- common.resource_reference: a message and its references live in two schemas, and
    -- writing both atomically stops being possible once the modules are split apart.
    "attachments" BIGINT[] NOT NULL DEFAULT '{}',
    -- Referenced ids: listing / variant / order, and `{"offer_id": N}` for a price
    -- negotiation. The offer's terms are NOT copied here — order.offer is the source of truth
    -- and this message only says which card to render, so a revision cannot leave the thread
    -- showing a price that is no longer on the table.
    "metadata" JSONB NOT NULL DEFAULT '{}',
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "edited_at" TIMESTAMPTZ,
    "deleted_at" TIMESTAMPTZ, -- redaction: the sender unsending, or moderation acting on a report

    -- "created_at" joins the key because every unique index on a hypertable has to
    -- contain its partitioning column.
    CONSTRAINT "message_pkey" PRIMARY KEY ("id", "created_at"),
    CONSTRAINT "message_sender_matches_type" CHECK (
        ("type" = 'system') = ("sender_id" IS NULL)
    ),

    CONSTRAINT "message_conversation_id_fkey" FOREIGN KEY ("conversation_id")
        REFERENCES "conversation" ("id") ON DELETE CASCADE
);
-- Chunked by time so index maintenance and vacuum stay bounded on the biggest table here.
-- No retention policy on purpose: messages are evidence in refund disputes, so ageing
-- them out is a product and legal call. add_retention_policy('message', …) if that changes.
SELECT create_hypertable('message', 'created_at', if_not_exists => TRUE);

-- Message history in a thread, newest first. Paginate on a "created_at" cursor rather
-- than an offset, so chunk exclusion can skip chunks instead of scanning all of them.
-- Also the unread count: "conversation_id" plus a lower bound on "created_at" from the
-- reader's mark on "conversation" is a range on this index, and the bound is what lets
-- chunk exclusion skip everything older.
CREATE INDEX IF NOT EXISTS "message_conversation_id_created_at_idx"
    ON "message" ("conversation_id", "created_at" DESC);
-- Moderation: everything one account has sent.
CREATE INDEX IF NOT EXISTS "message_sender_id_idx"
    ON "message" ("sender_id", "created_at" DESC);
