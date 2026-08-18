-- The personalised feed's other source, next to "favorite": every view and click a shopper's
-- own subscriber (catalog.event.go) turns into a row here, off the same
-- catalog.listing_interaction fact observability's popularity score reads independently. One
-- row per action, not one per (account, listing) — a listing looked at three times says more
-- about a taste than one looked at once, which "favorite" cannot express since saving is a
-- single fact.
--
-- "not-interested" and "hidden" land here too, but interestSignals never averages their weight
-- in: it excludes the listing outright (see RecomputeInterests). A negative number has no
-- business in an average that becomes a share of the page.
CREATE TABLE IF NOT EXISTS "listing_signal" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "account_id" BIGINT NOT NULL,
    "listing_id" BIGINT NOT NULL,
    "type" TEXT NOT NULL,
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "listing_signal_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "listing_signal_listing_id_fkey" FOREIGN KEY ("listing_id")
        REFERENCES "listing" ("id") ON DELETE CASCADE
);
-- interestSignals' own read: an account's most recent actions, most recent first.
CREATE INDEX IF NOT EXISTS "listing_signal_account_id_created_at_idx"
    ON "listing_signal" ("account_id", "created_at" DESC);
-- RecomputeInterests' exclusion check: has this account marked this listing not-interested or
-- hidden. Different leading columns than the index above on purpose — that one orders by time
-- for one account, this one asks about one listing for one account and does not care when.
CREATE INDEX IF NOT EXISTS "listing_signal_account_id_listing_id_idx"
    ON "listing_signal" ("account_id", "listing_id");
