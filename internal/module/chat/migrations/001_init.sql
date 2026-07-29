-- Module: chat — canonical schema
-- Description: Real-time messaging between accounts. Each conversation is a 1-1
--              thread, one per pair of accounts regardless of who buys or sells.
--              Messages carry text, attachments and backend-generated events.

-- "message" is a hypertable, so the extension has to exist before it is created.
-- chat migrates before observability, which cannot be relied on to have added it.
CREATE EXTENSION IF NOT EXISTS timescaledb;

-- Enums
-- Delivery state of a message (server-side tracking)
CREATE TYPE "message_status" AS ENUM ('sent', 'delivered', 'read');
-- Who produced the message. 'system' rows come from the backend (an offer was
-- accepted, an order shipped) and belong to no participant.
CREATE TYPE "message_type" AS ENUM ('user', 'system');

-- Tables

CREATE TABLE IF NOT EXISTS "audit_log" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "version" BIGINT NOT NULL DEFAULT 1, -- Incremented on each change to the same record
    "table_name" VARCHAR(100) NOT NULL,
    "record_id" BIGINT NOT NULL,
    "change_type" VARCHAR(10) NOT NULL, -- 'insert', 'update', 'delete'
    "code" VARCHAR(100) NOT NULL, -- e.g. Business code 'message.redact', 'conversation.create'
    "changed_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "changed_by" BIGINT, -- account_id of the user who made the change (if applicable)
    "diff" JSONB NOT NULL, -- JSON diff of the record's fields (for insert only, other diff = snapshot)
    "snapshot" JSONB NOT NULL, -- Full record values after the change
    CONSTRAINT "audit_log_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "audit_log_table_name_record_id_version_key" UNIQUE ("table_name", "record_id", "version")
);

-- One thread per pair of accounts, whoever is buying. The pair is stored ordered so the
-- unique constraint cannot be sidestepped by swapping sides, and that CHECK also rules
-- out a thread with oneself. Not listing-scoped: products are referenced per message.
CREATE TABLE IF NOT EXISTS "conversation" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "account_a_id" BIGINT NOT NULL, -- cross-ref account.account; no FK
    "account_b_id" BIGINT NOT NULL, -- cross-ref account.account; no FK
    -- Denormalized for inbox ordering, maintained by the service. Starts at
    -- "created_at" so an empty thread still sorts predictably.
    "last_message_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "conversation_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "conversation_pair_key" UNIQUE ("account_a_id", "account_b_id"),
    CONSTRAINT "conversation_pair_ordered" CHECK ("account_a_id" < "account_b_id")
);
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
    "status" "message_status" NOT NULL DEFAULT 'sent',
    -- Resource ids owned by the common module, held inline rather than through
    -- common.resource_reference: a message and its references live in two schemas, and
    -- writing both atomically stops being possible once the modules are split apart.
    "attachments" BIGINT[] NOT NULL DEFAULT '{}',
    "metadata" JSONB NOT NULL DEFAULT '{}', -- referenced spu / sku / order ids, offer payloads
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
CREATE INDEX IF NOT EXISTS "message_conversation_id_created_at_idx"
    ON "message" ("conversation_id", "created_at" DESC);
-- The unread badge: what the other party sent and this account has not read.
CREATE INDEX IF NOT EXISTS "message_unread_idx"
    ON "message" ("conversation_id", "sender_id")
    WHERE "status" <> 'read';
-- Moderation: everything one account has sent.
CREATE INDEX IF NOT EXISTS "message_sender_id_idx"
    ON "message" ("sender_id", "created_at" DESC);
