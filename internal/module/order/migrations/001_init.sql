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

-- Refund lifecycle (refund v2): buyer ships goods back at creation; seller
-- reviews within the deadline; disputes escalate to admin. 'cancelled' is a
-- buyer-withdraw terminal state
CREATE TYPE "refund_status" AS ENUM (
    'shipping',
    'awaiting-seller-review',
    'disputed',
    'accepted',
    'rejected',
    'cancelled'
);

CREATE TYPE "dispute_status" AS ENUM (
    'open',
    'seller-wins',
    'buyer-wins'
);

CREATE TYPE "offer_status" AS ENUM ('active', 'accepted', 'cancelled');

-- Tables
CREATE TABLE
  IF NOT EXISTS "audit_log" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "version" BIGINT NOT NULL DEFAULT 1, -- Incremented on each change to the same record
    "table_name" VARCHAR(100) NOT NULL,
    "record_id" BIGINT NOT NULL,
    "change_type" VARCHAR(10) NOT NULL, -- 'insert', 'update', 'delete'
    "code" VARCHAR(100) NOT NULL, -- e.g. Business code 'product_spu.publish', 'comment.delete', 'account.suspend'
    "changed_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "changed_by" BIGINT, -- account_id of the user who made the change (if applicable)
    "diff" JSONB NOT NULL, -- JSON diff of the record's fields (for insert only, other diff = snapshot)
    "snapshot" JSONB NOT NULL, -- Full record values after the change
    CONSTRAINT "audit_log_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "audit_log_table_name_record_id_version_key" UNIQUE ("table_name", "record_id", "version")
  );

-- Flat shopping cart: one row per (account, SKU) pair.
CREATE TABLE IF NOT EXISTS "cart_item" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "account_id" BIGINT NOT NULL,
    "sku_id" BIGINT NOT NULL,
    "quantity" BIGINT NOT NULL,
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, -- sorts the cart, and ages out stale ones

    CONSTRAINT "cart_item_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "cart_item_account_id_sku_id_key" UNIQUE ("account_id", "sku_id"),
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
    "spu_id" BIGINT NOT NULL, -- Aggregate root id (not sku_id)
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

-- Checkout item: starts unconfirmed (order_id IS NULL), linked to an order on seller confirmation.
CREATE TABLE IF NOT EXISTS "item" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "draft_id" BIGINT NOT NULL, -- The purchase session this was checked out in
    "order_id" BIGINT, -- NULL until the seller confirms
    "buyer_id" BIGINT NOT NULL,
    "seller_id" BIGINT NOT NULL, -- Denormalized from sku->spu->seller
    "sku_id" BIGINT NOT NULL,
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
CREATE INDEX IF NOT EXISTS "item_sku_id_idx" ON "item" ("sku_id");
CREATE INDEX IF NOT EXISTS "item_draft_id_idx" ON "item" ("draft_id");
-- The payment webhook's first lookup: which items did this session pay for.
CREATE INDEX IF NOT EXISTS "item_payment_session_id_idx" ON "item" ("payment_session_id");
-- "My purchases", newest first.
CREATE INDEX IF NOT EXISTS "item_buyer_id_idx" ON "item" ("buyer_id", "created_at" DESC);
-- Seller's pending inbox: paid items awaiting confirmation
CREATE INDEX IF NOT EXISTS "idx_item_seller_pending" ON "item" ("seller_id", "transport_option") WHERE "order_id" IS NULL AND "cancelled_at" IS NULL;

-- Refund request raised by the buyer. The buyer ships the goods back at
-- creation time (return_transport_id is required), so by the time the seller
-- sees AwaitingSellerReview they already have the physical items.
CREATE TABLE IF NOT EXISTS "refund" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "buyer_id" BIGINT NOT NULL, -- buyer
    "order_id" BIGINT NOT NULL,
    "reason" TEXT NOT NULL,
    -- Evidence the buyer attaches when opening the refund; resource ids from common.
    -- A dispute is decided on these, so they must outlive the refund flow.
    "attachments" BIGINT[] NOT NULL DEFAULT '{}',
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    "status" "refund_status" NOT NULL DEFAULT 'shipping',

    -- Forward leg: buyer → seller (mandatory, set at create)
    "return_transport_id" BIGINT NOT NULL,
    "received_by_seller_at" TIMESTAMPTZ, -- set when return transport hits success
    "review_deadline_at" TIMESTAMPTZ, -- date_received + 3D, auto-accept timer

    -- Seller decision (accepted / disputed)
    "seller_decided_at" TIMESTAMPTZ,

    -- Rejection backflow: admin upheld seller → ship goods back
    "return_to_buyer_transport_id" BIGINT,
    "rejection_reason" TEXT,

    "refund_tx_id" BIGINT, -- set only when accepted (the negative-amount reversal leg)

    CONSTRAINT "refund_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "refund_return_transport_id_key" UNIQUE ("return_transport_id"),
    CONSTRAINT "refund_return_to_buyer_transport_id_key" UNIQUE ("return_to_buyer_transport_id"),

    CONSTRAINT "refund_order_id_fkey" FOREIGN KEY ("order_id")
        REFERENCES "order" ("id") ON DELETE NO ACTION,
    CONSTRAINT "refund_return_transport_id_fkey" FOREIGN KEY ("return_transport_id")
        REFERENCES "transport" ("id") ON DELETE NO ACTION,
    CONSTRAINT "refund_return_to_buyer_transport_id_fkey" FOREIGN KEY ("return_to_buyer_transport_id")
        REFERENCES "transport" ("id") ON DELETE NO ACTION
);
CREATE INDEX IF NOT EXISTS "refund_buyer_id_idx" ON "refund" ("buyer_id");
CREATE INDEX IF NOT EXISTS "refund_order_id_idx" ON "refund" ("order_id");
CREATE INDEX IF NOT EXISTS "refund_status_idx" ON "refund" ("status");
CREATE UNIQUE INDEX IF NOT EXISTS "refund_one_active_per_order"
    ON "refund" ("order_id")
    WHERE "status" IN ('shipping', 'awaiting-seller-review', 'disputed');
-- The auto-accept job: refunds whose review window has run out.
CREATE INDEX IF NOT EXISTS "refund_review_deadline_idx"
    ON "refund" ("review_deadline_at")
    WHERE "status" = 'awaiting-seller-review';

-- Seller-initiated escalation when seller refuses the refund after physical
-- inspection. Admin resolves: seller-wins → refund rejected; buyer-wins → refund accepted.
CREATE TABLE IF NOT EXISTS "refund_dispute" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "refund_id" BIGINT NOT NULL,
    "account_id" BIGINT NOT NULL, -- seller (the disputer)
    "reason" TEXT NOT NULL,
    -- The seller's side of the evidence, kept apart from "refund"."attachments" so
    -- the admin can tell who submitted what.
    "attachments" BIGINT[] NOT NULL DEFAULT '{}',
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    "status" "dispute_status" NOT NULL DEFAULT 'open',

    "resolved_by_id" BIGINT, -- admin
    "resolved_at" TIMESTAMPTZ,
    "resolution_note" TEXT,

    CONSTRAINT "refund_dispute_pkey" PRIMARY KEY ("id"),

    CONSTRAINT "refund_dispute_refund_id_fkey" FOREIGN KEY ("refund_id")
        REFERENCES "refund" ("id") ON DELETE NO ACTION
);
CREATE UNIQUE INDEX IF NOT EXISTS "refund_dispute_one_active_per_refund"
    ON "refund_dispute" ("refund_id")
    WHERE "status" = 'open';
CREATE INDEX IF NOT EXISTS "refund_dispute_account_id_idx" ON "refund_dispute" ("account_id");
CREATE INDEX IF NOT EXISTS "refund_dispute_status_idx" ON "refund_dispute" ("status");

-- Order cancellation is derived in the order service (was a DB function; moved to domain).

-- One negotiation per (buyer, sku); current terms updated in place.
CREATE TABLE IF NOT EXISTS "offer" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "sku_id" BIGINT NOT NULL, -- cross-ref catalog.product_sku; no FK
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
CREATE UNIQUE INDEX IF NOT EXISTS "offer_one_active_per_buyer_sku" ON "offer" ("buyer_id", "sku_id") WHERE "status" = 'active';
-- The expiry job: live offers past their deadline.
CREATE INDEX IF NOT EXISTS "offer_expiring_idx"
    ON "offer" ("expires_at")
    WHERE "status" = 'active';
CREATE INDEX IF NOT EXISTS "offer_seller_id_status_idx" ON "offer" ("seller_id", "status");
CREATE INDEX IF NOT EXISTS "offer_buyer_id_status_idx" ON "offer" ("buyer_id", "status");
CREATE INDEX IF NOT EXISTS "offer_sku_id_idx" ON "offer" ("sku_id");
