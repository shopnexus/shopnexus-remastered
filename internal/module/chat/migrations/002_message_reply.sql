-- Answering one message in particular.
--
-- A negotiation thread is where this is missed: "còn cái này không?" is unreadable unless
-- the reader knows which of four photos it answers, and the only way to say which was to
-- retype it. A reply names the message instead, and the quote is resolved live rather than
-- copied — the same reason an offer card stores only "offer_id" and never its terms, so an
-- edited original cannot leave a stale quote on screen and a redacted one reads as redacted.

ALTER TABLE "message"
    -- The message this one answers, NULL on an ordinary one. Two columns because "message"
    -- is a hypertable whose primary key is (id, created_at): the instant is what turns
    -- resolving a quote into a point lookup in one chunk instead of a scan across all of
    -- them, which is the same reason the edit and redact routes take it.
    --
    -- No foreign key. TimescaleDB does not support one pointing *at* a hypertable, and the
    -- rule that actually matters is not referential anyway: the target must be in the same
    -- conversation, or a quote would carry a preview of somebody else's thread. The service
    -- checks that before the INSERT.
    ADD COLUMN IF NOT EXISTS "reply_to_id" BIGINT,
    ADD COLUMN IF NOT EXISTS "reply_to_created_at" TIMESTAMPTZ;

ALTER TABLE "message"
    DROP CONSTRAINT IF EXISTS "message_reply_to_complete";

-- Both or neither: half a reference resolves to nothing, and a NULL instant would send the
-- lookup scanning every chunk to discover that.
ALTER TABLE "message"
    ADD CONSTRAINT "message_reply_to_complete" CHECK (
        ("reply_to_id" IS NULL) = ("reply_to_created_at" IS NULL)
    );
