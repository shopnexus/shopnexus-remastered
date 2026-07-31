-- Module: order — canonical schema
-- Description: Shopping cart, checkout items, payments, transport/delivery,
--              orders, refunds, and refund disputes. Any account can be
--              buyer (buyer_id) or seller (seller_id) within a single order.

-- Enums

-- Shipment lifecycle as a carrier reports it. A generic pending/success enum left the
-- intermediate legs inside "data" where nothing could query them.
CREATE TYPE "transport_status" AS ENUM (
    'pending',     -- created, not yet handed to the carrier
    'picked-up',
    'in-transit',
    'delivered',
    'returned',    -- came back to the sender
    'failed',
    'cancelled'
);

-- Refund lifecycle. A refund always covers the whole order, so there is no partial
-- amount anywhere in this flow.
--
-- Every non-terminal value is named for the party whose move the refund is waiting
-- on, and each of those carries a "deadline_at". That is what makes one job able to
-- advance all of them, and what makes "who is holding this up" answerable without
-- reading the service.
--
--   awaiting-seller-review  the seller accepts or rejects
--   awaiting-buyer-action   the buyer escalates to a moderator, or lets it lapse.
--                           Entered by a rejection, or by the seller letting the
--                           review window pass; "rejection_reason" tells them apart.
--   disputed                a moderator rules on the open round of "refund_dispute"
--   returning               the carrier delivers the goods back to the seller. The
--                           return leg exists only from here — a refund that never
--                           gets granted never ships anything.
--   returned                the seller inspects what arrived and may appeal, which is
--                           round 2 of the same dispute; letting the window pass
--                           settles the refund for the buyer.
CREATE TYPE "refund_status" AS ENUM (
    'awaiting-seller-review',
    'awaiting-buyer-action',
    'disputed',
    'returning',
    'returned',
    'accepted',  -- terminal: the money went back to the buyer
    'rejected',  -- terminal: no refund, and the payout to the seller stands
    'cancelled'  -- terminal: the buyer withdrew before the seller decided
);

-- The winner of one dispute round, whichever round it is: in round 1 'buyer-wins'
-- grants the refund and starts the return, in round 2 it denies the seller's appeal
-- and settles. 'seller-wins' ends the refund in both.
CREATE TYPE "dispute_status" AS ENUM (
    'open',
    'seller-wins',
    'buyer-wins'
);

CREATE TYPE "offer_status" AS ENUM ('active', 'accepted', 'cancelled');


-- Flat shopping cart: one row per (account, SKU) pair.
CREATE TABLE IF NOT EXISTS "cart_item" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "account_id" BIGINT NOT NULL,
    -- Aggregate root of the SKU, denormalized on insert like "item"."seller_id" is.
    -- Rendering a cart means reading listings, and the variant is not addressable on
    -- its own in the catalog API, so without this a cart row cannot be resolved at all.
    "listing_id" BIGINT NOT NULL,
    "variant_id" BIGINT NOT NULL,
    "quantity" BIGINT NOT NULL,
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, -- sorts the cart, and ages out stale ones

    CONSTRAINT "cart_item_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "cart_item_account_id_variant_id_key" UNIQUE ("account_id", "variant_id"),
    CONSTRAINT "cart_item_quantity_positive" CHECK ("quantity" > 0)
);

-- payment_session, transaction, and the transaction_settled view moved to the
-- payment module (money primitives live together for atomicity). order refers
-- to them by id only: confirm_session_id / payout_session_id (order),
-- payment_session_id (item, offer), refund_tx_id (refund) — no cross-schema FK.

-- Transport/delivery record
CREATE TABLE IF NOT EXISTS "transport" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "option" VARCHAR(100) NOT NULL, -- References common.option (transport); same kebab-case slug
    "status" "transport_status" NOT NULL DEFAULT 'pending',
    "data" JSONB NOT NULL DEFAULT '{}', -- Provider-specific data (tracking number, label URL, webhook events, etc.)
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "transport_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "transport_option_format" CHECK ("option" ~ '^[a-z0-9]+(-[a-z0-9]+)*$')
);

-- A buyer's purchase session for one product: it freezes the terms, so a listing that
-- showed 100k cannot charge a newly-updated price at confirmation. Its items hang off it
-- until a seller confirms them into an "order".
CREATE TABLE IF NOT EXISTS "draft_order" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "buyer_id" BIGINT NOT NULL,
    "listing_id" BIGINT NOT NULL, -- Aggregate root id (not variant_id)
    -- The listing when the session opened: { "spu": {…}, "skus": [{ id, price,
    -- attributes, package_details }] }. Carries the SKUs because price and shipping
    -- weight live there, and those are what must not move under the buyer.
    "spu_snapshot" JSONB NOT NULL,

    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "cancelled_at" TIMESTAMPTZ, -- Set when the draft order is cancelled
    "valid_until" TIMESTAMPTZ NOT NULL, -- Expiration timestamp for the draft order

    CONSTRAINT "draft_order_pkey" PRIMARY KEY ("id")
);
-- The expiry job: live sessions past their deadline.
CREATE INDEX IF NOT EXISTS "draft_order_expiring_idx"
    ON "draft_order" ("valid_until")
    WHERE "cancelled_at" IS NULL;
CREATE INDEX IF NOT EXISTS "draft_order_buyer_id_idx" ON "draft_order" ("buyer_id", "created_at" DESC);

-- Order created when a seller confirms pending items.
CREATE TABLE IF NOT EXISTS "order" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "draft_id" BIGINT NOT NULL, -- The draft order that was confirmed to create this order
    "buyer_id" BIGINT NOT NULL,
    "transport_id" BIGINT NOT NULL,
    -- Contact snapshot shaped like account.contact. JSONB not text: the administrative
    -- codes are what a carrier is called with, and account.contact may have changed since.
    "address" JSONB NOT NULL,
    "pickup_address" JSONB NOT NULL, -- Seller's collection point, snapshotted the same way

    -- Seller confirmation of the order
    "confirm_session_id" BIGINT, -- Seller confirmation shipping fee session (if seller pays the shipping)
    "note" TEXT, -- Seller note

    -- Buyer's receipt confirmation. The unboxing evidence lives here rather than in a
    -- side table because it is captured in the same request that sets "received_at" and
    -- is never added to afterwards — a refund is judged on what the buyer showed at the
    -- moment of unboxing, so a growable list would weaken the record it exists to be.
    "received_at" TIMESTAMPTZ,
    "receipt_attachments" BIGINT[] NOT NULL DEFAULT '{}', -- resource ids from common
    -- The escrow release to the seller, 72h after receipt unless a refund intervenes.
    -- Set by the payout job; its presence is what stops the job paying twice.
    "payout_session_id" BIGINT,

    -- Denormalized
    "seller_id" BIGINT NOT NULL, -- Denormalized from order items for easier querying;
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    -- Outcome facts, not a status column: the lifecycle stays in the service, but these
    -- make "my open orders" an index lookup instead of a join across three tables.
    "completed_at" TIMESTAMPTZ,
    "cancelled_at" TIMESTAMPTZ,

    CONSTRAINT "order_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "order_transport_id_key" UNIQUE ("transport_id"),
    -- One order per purchase session, so confirming twice cannot mint a second order.
    CONSTRAINT "order_draft_id_key" UNIQUE ("draft_id"),
    -- Confirming receipt and showing the goods are one act; neither half is a state.
    CONSTRAINT "order_receipt_attachments_match_received" CHECK (
        ("received_at" IS NOT NULL) = (cardinality("receipt_attachments") > 0)
    ),
    -- Money is only released against a confirmed delivery.
    CONSTRAINT "order_payout_needs_receipt" CHECK (
        "payout_session_id" IS NULL OR "received_at" IS NOT NULL
    ),

    CONSTRAINT "order_transport_id_fkey" FOREIGN KEY ("transport_id")
        REFERENCES "transport" ("id") ON DELETE NO ACTION,
    CONSTRAINT "order_draft_id_fkey" FOREIGN KEY ("draft_id")
        REFERENCES "draft_order" ("id") ON DELETE NO ACTION
);
CREATE INDEX IF NOT EXISTS "order_transport_id_idx" ON "order" ("transport_id");
-- Buyer's and seller's order lists, newest first, open ones only. Partial because an
-- open order is the small, hot slice; history is read by id or by explicit date range.
CREATE INDEX IF NOT EXISTS "order_buyer_id_open_idx"
    ON "order" ("buyer_id", "created_at" DESC)
    WHERE "completed_at" IS NULL AND "cancelled_at" IS NULL;
CREATE INDEX IF NOT EXISTS "order_seller_id_open_idx"
    ON "order" ("seller_id", "created_at" DESC)
    WHERE "completed_at" IS NULL AND "cancelled_at" IS NULL;
CREATE INDEX IF NOT EXISTS "order_buyer_id_idx" ON "order" ("buyer_id", "created_at" DESC);
CREATE INDEX IF NOT EXISTS "order_seller_id_idx" ON "order" ("seller_id", "created_at" DESC);
-- The payout job: receipts confirmed, nothing paid out yet, oldest first. The 72h
-- offset is applied in the query rather than stored, so changing the window does not
-- need a migration or a backfill. The job still has to skip an order with a live
-- refund, which "refund_one_active_per_order" answers.
CREATE INDEX IF NOT EXISTS "order_payout_due_idx"
    ON "order" ("received_at")
    WHERE "payout_session_id" IS NULL AND "received_at" IS NOT NULL AND "cancelled_at" IS NULL;

-- Checkout item: starts unconfirmed (order_id IS NULL), linked to an order on seller confirmation.
CREATE TABLE IF NOT EXISTS "item" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "draft_id" BIGINT NOT NULL, -- The purchase session this was checked out in
    "order_id" BIGINT, -- NULL until the seller confirms
    "buyer_id" BIGINT NOT NULL,
    "seller_id" BIGINT NOT NULL, -- Denormalized from sku->spu->seller
    "listing_id" BIGINT NOT NULL, -- The same hop's midpoint, kept so order history can resolve the listing
    "variant_id" BIGINT NOT NULL,
    "address" JSONB NOT NULL, -- Delivery contact snapshot, same shape as "order"."address"
    "note" TEXT, -- Buyer note
    "currency" VARCHAR(3) NOT NULL, -- Currency the SPU was originally priced in; combined with session.fx_snapshot to replay conversion

    -- PAY-FIRST
    "quantity" BIGINT NOT NULL,
    "transport_option" VARCHAR(100) NOT NULL, -- References common.option (transport); same kebab-case slug
    "total_amount" BIGINT NOT NULL, -- Final paid amount in session.currency after discounts, taxes, etc. Used for display & refunds
    "payment_session_id" BIGINT NOT NULL,

    -- Cancellation
    "cancelled_at" TIMESTAMPTZ, -- Set when buyer or seller cancels the item
    "cancelled_by_id" BIGINT, -- Account that cancelled the item (buyer or seller, NULL means system)

    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "item_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "item_quantity_positive" CHECK ("quantity" > 0),
    CONSTRAINT "item_total_amount_non_negative" CHECK ("total_amount" >= 0),
    CONSTRAINT "item_currency_format" CHECK ("currency" ~ '^[A-Z]{3}$'),
    CONSTRAINT "item_transport_option_format" CHECK ("transport_option" ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),

    -- NO ACTION, not CASCADE: this row records money the buyer paid, so deleting an
    -- order must not erase it.
    CONSTRAINT "item_order_id_fkey" FOREIGN KEY ("order_id")
        REFERENCES "order" ("id") ON DELETE NO ACTION,
    CONSTRAINT "item_draft_id_fkey" FOREIGN KEY ("draft_id")
        REFERENCES "draft_order" ("id") ON DELETE NO ACTION
);
CREATE INDEX IF NOT EXISTS "item_order_id_idx" ON "item" ("order_id");
CREATE INDEX IF NOT EXISTS "item_variant_id_idx" ON "item" ("variant_id");
CREATE INDEX IF NOT EXISTS "item_draft_id_idx" ON "item" ("draft_id");
-- The payment webhook's first lookup: which items did this session pay for.
CREATE INDEX IF NOT EXISTS "item_payment_session_id_idx" ON "item" ("payment_session_id");
-- "My purchases", newest first.
CREATE INDEX IF NOT EXISTS "item_buyer_id_idx" ON "item" ("buyer_id", "created_at" DESC);
-- Seller's confirmation inbox: paid items no order covers yet, newest first. Ordered on
-- "created_at" because that is how the inbox is read and paged; grouping the same
-- carrier together is the confirm step's business, and it works from a page this
-- already returned rather than from the index.
CREATE INDEX IF NOT EXISTS "item_seller_pending_idx"
    ON "item" ("seller_id", "created_at" DESC)
    WHERE "order_id" IS NULL AND "cancelled_at" IS NULL;

-- Refund request raised by the buyer, always for the whole order. No amount column:
-- the sum is the order's checkout session total, and storing a second copy of it
-- would let the two disagree about how much is owed.
--
-- Nothing ships at creation. The buyer keeps the goods until the refund is actually
-- granted — by the seller accepting or by a moderator ruling for the buyer — because
-- most requests are settled or refused without a parcel ever moving, and a return
-- posted up front would have to be un-posted every time.
CREATE TABLE IF NOT EXISTS "refund" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "buyer_id" BIGINT NOT NULL,
    "order_id" BIGINT NOT NULL,
    "reason" TEXT NOT NULL,
    -- The buyer's evidence, added at creation and topped up until the case closes.
    -- Resource ids from common. A dispute is decided on these, so they outlive the flow.
    "attachments" BIGINT[] NOT NULL DEFAULT '{}',
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    "status" "refund_status" NOT NULL DEFAULT 'awaiting-seller-review',
    -- When the party named by "status" runs out of time. NULL in the states nobody is
    -- on the clock for: 'disputed' waits on a moderator and 'returning' on a carrier,
    -- neither of which a timer should decide, and the terminal states wait on nothing.
    "deadline_at" TIMESTAMPTZ,

    -- Seller review round. "rejection_reason" is what separates a refusal from a
    -- seller who simply let the window pass: both land on the buyer, only one has a
    -- reason to show them.
    "seller_decided_at" TIMESTAMPTZ,
    "rejection_reason" TEXT,

    -- Return leg: buyer → seller, created when the refund is granted, never before.
    -- No leg back to the buyer: a seller who wins round 1 was never sent anything, and
    -- one who wins round 2 is holding goods a moderator has just called not-as-sent.
    "return_transport_id" BIGINT,
    "returned_at" TIMESTAMPTZ, -- set when the return transport reaches delivered

    "refund_tx_id" BIGINT, -- the negative-amount reversal leg; set only on 'accepted'

    CONSTRAINT "refund_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "refund_return_transport_id_key" UNIQUE ("return_transport_id"),
    -- Each fact needs the one before it. Without these a refund can be delivered back
    -- with no parcel, or paid out with no verdict.
    CONSTRAINT "refund_returned_needs_transport" CHECK (
        "returned_at" IS NULL OR "return_transport_id" IS NOT NULL
    ),
    CONSTRAINT "refund_rejection_needs_decision" CHECK (
        "rejection_reason" IS NULL OR "seller_decided_at" IS NOT NULL
    ),
    CONSTRAINT "refund_tx_only_when_accepted" CHECK (
        "refund_tx_id" IS NULL OR "status" = 'accepted'
    ),
    -- A live refund always has someone on the clock, and the two states that wait on a
    -- carrier or a moderator never do.
    CONSTRAINT "refund_deadline_matches_status" CHECK (
        ("deadline_at" IS NOT NULL) =
        ("status" IN ('awaiting-seller-review', 'awaiting-buyer-action', 'returned'))
    ),

    CONSTRAINT "refund_order_id_fkey" FOREIGN KEY ("order_id")
        REFERENCES "order" ("id") ON DELETE NO ACTION,
    CONSTRAINT "refund_return_transport_id_fkey" FOREIGN KEY ("return_transport_id")
        REFERENCES "transport" ("id") ON DELETE NO ACTION
);
CREATE INDEX IF NOT EXISTS "refund_buyer_id_idx" ON "refund" ("buyer_id", "created_at" DESC);
CREATE INDEX IF NOT EXISTS "refund_order_id_idx" ON "refund" ("order_id");
-- One live refund per order, so a second request while the first is open is a conflict
-- rather than a second claim on the same money.
CREATE UNIQUE INDEX IF NOT EXISTS "refund_one_active_per_order"
    ON "refund" ("order_id")
    WHERE "status" IN ('awaiting-seller-review', 'awaiting-buyer-action', 'disputed', 'returning', 'returned');
-- One job advances every overdue refund, and it reads one index to find them all:
-- a missed seller review moves to the buyer, a buyer who never escalated lapses to
-- 'rejected', and an uncontested return settles as 'accepted'. Which of the three it
-- is follows from "status", so the timer does not need a column per state.
CREATE INDEX IF NOT EXISTS "refund_overdue_idx"
    ON "refund" ("deadline_at")
    WHERE "deadline_at" IS NOT NULL;

-- One round of moderation on a refund. Two are possible and both are kept:
--   round 1  the buyer escalating, after a rejection or an ignored review
--   round 2  the seller appealing what came back — a counterfeit, or the wrong item
-- A second row rather than a second verdict on the first, so that ruling for the buyer
-- and then for the seller stays legible as two decisions instead of one that changed
-- its mind. "opened_by_id" says whose round it is, and the round's "attachments" hold
-- that side's evidence, kept apart from the buyer's on "refund" so a moderator can
-- always tell who submitted what.
CREATE TABLE IF NOT EXISTS "refund_dispute" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "refund_id" BIGINT NOT NULL,
    "round" SMALLINT NOT NULL,
    "opened_by_id" BIGINT NOT NULL, -- the buyer in round 1, the seller in round 2
    "reason" TEXT NOT NULL,
    "attachments" BIGINT[] NOT NULL DEFAULT '{}',
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    "status" "dispute_status" NOT NULL DEFAULT 'open',

    "resolved_by_id" BIGINT, -- the moderator
    "resolved_at" TIMESTAMPTZ,
    "resolution_note" TEXT,

    CONSTRAINT "refund_dispute_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "refund_dispute_refund_id_round_key" UNIQUE ("refund_id", "round"),
    CONSTRAINT "refund_dispute_round_range" CHECK ("round" IN (1, 2)),
    -- Being open and being unresolved are the same fact, and a verdict always has an
    -- author.
    CONSTRAINT "refund_dispute_resolution_together" CHECK (
        (("status" = 'open') = ("resolved_at" IS NULL))
        AND (("resolved_at" IS NULL) = ("resolved_by_id" IS NULL))
    ),

    CONSTRAINT "refund_dispute_refund_id_fkey" FOREIGN KEY ("refund_id")
        REFERENCES "refund" ("id") ON DELETE NO ACTION
);
-- At most one round open at a time: round 2 cannot be filed until round 1 is ruled.
CREATE UNIQUE INDEX IF NOT EXISTS "refund_dispute_one_open_per_refund"
    ON "refund_dispute" ("refund_id")
    WHERE "status" = 'open';
CREATE INDEX IF NOT EXISTS "refund_dispute_opened_by_id_idx" ON "refund_dispute" ("opened_by_id");
-- The moderator queue: open rounds, oldest first, which is the order they are worked.
CREATE INDEX IF NOT EXISTS "refund_dispute_queue_idx"
    ON "refund_dispute" ("created_at")
    WHERE "status" = 'open';

-- Order cancellation is derived in the order service (was a DB function; moved to domain).

-- One negotiation per (buyer, sku); current terms updated in place.
CREATE TABLE IF NOT EXISTS "offer" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "listing_id" BIGINT NOT NULL, -- cross-ref catalog.listing; the listing an offer card renders
    "variant_id" BIGINT NOT NULL, -- cross-ref catalog.variant; no FK
    "author_id" BIGINT NOT NULL, -- account that created the offer (buyer or seller)
    "buyer_id" BIGINT NOT NULL,
    "seller_id" BIGINT NOT NULL, -- denormalized from sku -> spu -> owner
    "status" "offer_status" NOT NULL DEFAULT 'active',
    "quantity" BIGINT NOT NULL,
    "total" BIGINT NOT NULL, -- current proposed price (the agreed terms once accepted)
    "reason" TEXT NOT NULL DEFAULT '', -- offer-card note (e.g. discount reason)
    "payment_session_id" BIGINT, -- set on accept (auto-created checkout)

    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "expires_at" TIMESTAMPTZ NOT NULL,

    CONSTRAINT "offer_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "offer_total_positive" CHECK ("total" > 0),
    CONSTRAINT "offer_quantity_positive" CHECK ("quantity" > 0)
);
CREATE UNIQUE INDEX IF NOT EXISTS "offer_one_active_per_buyer_sku" ON "offer" ("buyer_id", "variant_id") WHERE "status" = 'active';
-- The expiry job: live offers past their deadline.
CREATE INDEX IF NOT EXISTS "offer_expiring_idx"
    ON "offer" ("expires_at")
    WHERE "status" = 'active';
CREATE INDEX IF NOT EXISTS "offer_seller_id_status_idx" ON "offer" ("seller_id", "status");
CREATE INDEX IF NOT EXISTS "offer_buyer_id_status_idx" ON "offer" ("buyer_id", "status");
CREATE INDEX IF NOT EXISTS "offer_variant_id_idx" ON "offer" ("variant_id");
