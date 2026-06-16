-- =============================================
-- Module: Order
-- Schema: order
-- Description: Shopping cart, checkout items, payments, transport/delivery,
--              orders, refunds, and refund disputes. Any account can be
--              buyer (buyer_id) or seller (seller_id) within a single order.
-- =============================================

CREATE SCHEMA IF NOT EXISTS "order";

-- Enums

-- Generic status used by payment, order, and transport tables
CREATE TYPE "order"."status" AS ENUM ('Pending', 'Processing', 'Success', 'Cancelled', 'Failed');

-- Refund lifecycle (refund v2): buyer ships goods back at creation; seller
-- reviews within the deadline; disputes escalate to admin. 'Cancelled' is a
-- buyer-withdraw terminal state
CREATE TYPE "order"."refund_status" AS ENUM (
    'Shipping',
    'AwaitingSellerReview',
    'Disputed',
    'Accepted',
    'Rejected',
    'Cancelled'
);

CREATE TYPE "order"."dispute_status" AS ENUM (
    'Open',
    'SellerWins',
    'BuyerWins'
);

-- Tables

-- Flat shopping cart: one row per (account, SKU) pair.
CREATE TABLE IF NOT EXISTS "order"."cart_item" (
    "id" BIGSERIAL NOT NULL,
    "account_id" UUID NOT NULL,
    "sku_id" UUID NOT NULL,
    "quantity" BIGINT NOT NULL,

    CONSTRAINT "cart_item_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "cart_item_account_id_sku_id_key" UNIQUE ("account_id", "sku_id")
);

-- Payment intent: one logical money flow (checkout, refund, payout, fee).
-- Mutable status; has 0..N transaction rows below for split-tender support.
CREATE TABLE IF NOT EXISTS "order"."payment_session" (
    "id" UUID NOT NULL, -- App-allocated UUID;
    "kind" TEXT NOT NULL, -- 'buyer-checkout' | 'seller-confirmation-fee' | 'seller-payout'; enum defined in app layer
    "status" "order"."status" NOT NULL,
    "from_id" UUID, -- Account initiating (buyer, seller, NULL = system)
    "to_id" UUID, -- Counterparty (buyer, seller, NULL = system)
    "note" TEXT NOT NULL,

    "currency" VARCHAR(3) NOT NULL, -- Buyer-facing currency; every child transaction settles via this currency
    "total_amount" BIGINT NOT NULL, -- Expected total in buyer-facing currency

    -- FX snapshot frozen at session creation. NULL when no cross-currency
    -- conversion was needed (every item already priced in `currency`).
    -- Shape: { "base": "USD", "rates": { "VND": "24500.0", ... }, "fetched_at": "..." }
    "fx_snapshot" JSONB,

    -- Checkout context shared across rails: cost breakdown, line items snapshot,
    -- applied promotions, gateway URLs per rail, provider metadata
    "data" JSONB NOT NULL,

    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "date_paid" TIMESTAMPTZ(3), -- Set when session reaches Success
    "date_expired" TIMESTAMPTZ(3) NOT NULL, -- Pending sessions auto-void after this timestamp

    CONSTRAINT "payment_session_pkey" PRIMARY KEY ("id")
);
CREATE INDEX IF NOT EXISTS "payment_session_kind_idx" ON "order"."payment_session" ("kind");
CREATE INDEX IF NOT EXISTS "payment_session_from_id_idx" ON "order"."payment_session" ("from_id");
CREATE INDEX IF NOT EXISTS "payment_session_status_pending_idx" ON "order"."payment_session" ("status") WHERE "status" IN ('Pending', 'Processing');

-- Append-only ledger leg: one row per rail movement (wallet debit, card charge,
-- refund leg). Status transitions Pending -> Success/Failed only; Success is terminal.
-- Reversals are NEW rows with negative amount + reverses_id pointing to the original.
CREATE TABLE IF NOT EXISTS "order"."transaction" (
    "id" UUID NOT NULL,
    "session_id" UUID NOT NULL,
    "status" "order"."status" NOT NULL,
    "note" TEXT NOT NULL,
    "error" TEXT,

    -- Concrete rail used. Both NULL = internal wallet (system credit / debit)
    "payment_option" TEXT, -- References common.option (payment)

    -- Rail-specific payload: gateway request/response, webhook payload, processor IDs
    "data" JSONB NOT NULL,

    -- Signed: positive = original charge; negative = reversal (refund leg).
    -- Per-rail currency because split-tender may mix currencies (e.g. wallet VND + card USD).
    -- Conversion rates live on payment_session.fx_snapshot, not per-tx.
    "amount" BIGINT NOT NULL,
    "currency" VARCHAR(3) NOT NULL, -- Currency the rail actually debits / credits

    -- Self-FK to the original charge this row reverses; NULL on originals.
    "reverses_id" UUID,

    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "date_settled" TIMESTAMPTZ(3), -- Set when status reaches Success
    "date_expired" TIMESTAMPTZ(3), -- Gateway URL expiry; NULL for internal wallet rails

    CONSTRAINT "transaction_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "transaction_sign_matches_reverses_chk" CHECK ((amount > 0 AND reverses_id IS NULL) OR (amount < 0 AND reverses_id IS NOT NULL)),
    CONSTRAINT "transaction_no_self_reverse_chk" CHECK (reverses_id IS NULL OR reverses_id != id),

    CONSTRAINT "transaction_session_id_fkey" FOREIGN KEY ("session_id")
        REFERENCES "order"."payment_session" ("id") ON DELETE NO ACTION ON UPDATE CASCADE,
    CONSTRAINT "transaction_reverses_id_fkey" FOREIGN KEY ("reverses_id")
        REFERENCES "order"."transaction" ("id") ON DELETE NO ACTION ON UPDATE CASCADE
);
CREATE INDEX IF NOT EXISTS "transaction_session_id_idx" ON "order"."transaction" ("session_id");
CREATE UNIQUE INDEX IF NOT EXISTS "transaction_reverses_id_unique" ON "order"."transaction" ("reverses_id") WHERE "reverses_id" IS NOT NULL;

-- Settled-only view for analytics / ledger queries; hides Pending/Failed rows.
-- Convention: revenue, refund total, dashboard SQL reads from this view, not the table.
CREATE OR REPLACE VIEW "order"."transaction_settled" AS
    SELECT * FROM "order"."transaction" WHERE "status" = 'Success';

-- Transport/delivery record
CREATE TABLE IF NOT EXISTS "order"."transport" (
    "id" BIGSERIAL NOT NULL,
    "option" TEXT NOT NULL, -- References common.option (transport)
    "status" "order"."status" DEFAULT 'Pending',
    "data" JSONB NOT NULL, -- Provider-specific data (tracking number, label URL, webhook events, etc.)
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "transport_pkey" PRIMARY KEY ("id")
);

-- Order created when a seller confirms pending items.
CREATE TABLE IF NOT EXISTS "order"."order" (
    "id" UUID NOT NULL DEFAULT gen_random_uuid(),
    "buyer_id" UUID NOT NULL,
    "seller_id" UUID NOT NULL, -- Denormalized from order items for easier querying;
    "transport_id" BIGINT NOT NULL,
    "address" TEXT NOT NULL,
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    -- Seller confirmation of the order
    "confirmed_by_id" UUID NOT NULL, -- Seller may have many accounts (staff)
    "confirm_session_id" UUID NOT NULL, -- Seller confirmation fee session (kind='seller-confirmation-fee')
    "note" TEXT, -- Seller note

    CONSTRAINT "order_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "order_transport_id_key" UNIQUE ("transport_id"),

    CONSTRAINT "order_transport_id_fkey" FOREIGN KEY ("transport_id")
        REFERENCES "order"."transport" ("id") ON DELETE NO ACTION ON UPDATE CASCADE,
    CONSTRAINT "order_confirm_session_id_fkey" FOREIGN KEY ("confirm_session_id")
        REFERENCES "order"."payment_session" ("id") ON DELETE NO ACTION ON UPDATE CASCADE
);
CREATE INDEX IF NOT EXISTS "order_buyer_id_idx" ON "order"."order" ("buyer_id");
CREATE INDEX IF NOT EXISTS "order_seller_id_idx" ON "order"."order" ("seller_id");
CREATE INDEX IF NOT EXISTS "order_transport_id_idx" ON "order"."order" ("transport_id");

-- Checkout item: starts unconfirmed (order_id IS NULL), linked to an order on seller confirmation.
CREATE TABLE IF NOT EXISTS "order"."item" (
    "id" BIGSERIAL NOT NULL,
    "order_id" UUID, -- NULL until the seller confirms
    "account_id" UUID NOT NULL,
    "seller_id" UUID NOT NULL, -- Denormalized from sku->spu->seller
    "sku_id" UUID NOT NULL,
    "spu_id" UUID NOT NULL, -- Snapshot of the SKU's parent SPU at time of purchase; used by review flows to scope comments to product family
    "sku_name" TEXT NOT NULL, -- Snapshot of SKU display name at time of purchase (prevents display issues if renamed)
    "address" TEXT NOT NULL, -- Snapshot of the delivery address
    "note" TEXT, -- Buyer note

    "serial_ids" JSONB, -- Array of assigned serial number IDs from inventory.serial (if serial_required)

    -- PAY-FIRST
    "quantity" BIGINT NOT NULL,
    "transport_option" TEXT NOT NULL,
    "subtotal_amount" BIGINT NOT NULL, -- quantity * unit price (converted to session.currency). Used for display
    "total_amount" BIGINT NOT NULL, -- Final paid amount in session.currency after discounts, taxes, etc. Used for display & refunds
    "source_currency" VARCHAR(3) NOT NULL, -- Currency the SPU was originally priced in; combined with session.fx_snapshot to replay conversion
    "payment_session_id" UUID NOT NULL,

    -- Cancellation
    "date_cancelled" TIMESTAMPTZ(3), -- Set when buyer or seller cancels the item
    "cancelled_by_id" UUID, -- Account that cancelled the item (buyer or seller, NULL means system)

    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "item_pkey" PRIMARY KEY ("id"),

    CONSTRAINT "item_order_id_fkey" FOREIGN KEY ("order_id")
        REFERENCES "order"."order" ("id") ON DELETE CASCADE ON UPDATE CASCADE,
    CONSTRAINT "item_payment_session_id_fkey" FOREIGN KEY ("payment_session_id")
        REFERENCES "order"."payment_session" ("id") ON DELETE NO ACTION ON UPDATE CASCADE
);
CREATE INDEX IF NOT EXISTS "item_order_id_idx" ON "order"."item" ("order_id");
CREATE INDEX IF NOT EXISTS "item_sku_id_idx" ON "order"."item" ("sku_id");
-- Seller's pending inbox: paid items awaiting confirmation
CREATE INDEX IF NOT EXISTS "idx_item_seller_pending" ON "order"."item" ("seller_id", "transport_option") WHERE "order_id" IS NULL AND "date_cancelled" IS NULL;

-- Refund request raised by the buyer. The buyer ships the goods back at
-- creation time (return_transport_id is required), so by the time the seller
-- sees AwaitingSellerReview they already have the physical items.
CREATE TABLE IF NOT EXISTS "order"."refund" (
    "id" UUID NOT NULL DEFAULT gen_random_uuid(),
    "account_id" UUID NOT NULL, -- buyer
    "order_id" UUID NOT NULL,
    "reason" TEXT NOT NULL,
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    "status" "order"."refund_status" NOT NULL DEFAULT 'Shipping',

    -- Forward leg: buyer → seller (mandatory, set at create)
    "return_transport_id" BIGINT NOT NULL,
    "date_received_by_seller" TIMESTAMPTZ(3), -- set when return transport hits Success
    "review_deadline" TIMESTAMPTZ(3), -- date_received + 3D, auto-accept timer

    -- Seller decision (Accepted / Disputed)
    "seller_decision_at" TIMESTAMPTZ(3),

    -- Rejection backflow: admin upheld seller → ship goods back
    "return_to_buyer_transport_id" BIGINT,
    "rejection_reason" TEXT,

    "refund_tx_id" UUID, -- set only when Accepted (the negative-amount reversal leg)

    CONSTRAINT "refund_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "refund_return_transport_id_key" UNIQUE ("return_transport_id"),
    CONSTRAINT "refund_return_to_buyer_transport_id_key" UNIQUE ("return_to_buyer_transport_id"),

    CONSTRAINT "refund_order_id_fkey" FOREIGN KEY ("order_id")
        REFERENCES "order"."order" ("id") ON DELETE NO ACTION ON UPDATE CASCADE,
    CONSTRAINT "refund_return_transport_id_fkey" FOREIGN KEY ("return_transport_id")
        REFERENCES "order"."transport" ("id") ON DELETE NO ACTION ON UPDATE CASCADE,
    CONSTRAINT "refund_return_to_buyer_transport_id_fkey" FOREIGN KEY ("return_to_buyer_transport_id")
        REFERENCES "order"."transport" ("id") ON DELETE NO ACTION ON UPDATE CASCADE,
    CONSTRAINT "refund_refund_tx_id_fkey" FOREIGN KEY ("refund_tx_id")
        REFERENCES "order"."transaction" ("id") ON DELETE NO ACTION ON UPDATE CASCADE
);
CREATE INDEX IF NOT EXISTS "refund_account_id_idx" ON "order"."refund" ("account_id");
CREATE INDEX IF NOT EXISTS "refund_order_id_idx" ON "order"."refund" ("order_id");
CREATE INDEX IF NOT EXISTS "refund_status_idx" ON "order"."refund" ("status");
CREATE UNIQUE INDEX IF NOT EXISTS "refund_one_active_per_order"
    ON "order"."refund" ("order_id")
    WHERE "status" IN ('Shipping', 'AwaitingSellerReview', 'Disputed');

-- Seller-initiated escalation when seller refuses the refund after physical
-- inspection. Admin resolves: SellerWins → refund Rejected; BuyerWins → refund Accepted.
CREATE TABLE IF NOT EXISTS "order"."refund_dispute" (
    "id" UUID NOT NULL DEFAULT gen_random_uuid(),
    "refund_id" UUID NOT NULL,
    "account_id" UUID NOT NULL, -- seller (the disputer)
    "reason" TEXT NOT NULL,
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    "status" "order"."dispute_status" NOT NULL DEFAULT 'Open',

    "resolved_by_id" UUID, -- admin
    "date_resolved" TIMESTAMPTZ(3),
    "resolution_note" TEXT,

    CONSTRAINT "refund_dispute_pkey" PRIMARY KEY ("id"),

    CONSTRAINT "refund_dispute_refund_id_fkey" FOREIGN KEY ("refund_id")
        REFERENCES "order"."refund" ("id") ON DELETE NO ACTION ON UPDATE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS "refund_dispute_one_active_per_refund"
    ON "order"."refund_dispute" ("refund_id")
    WHERE "status" = 'Open';
CREATE INDEX IF NOT EXISTS "refund_dispute_account_id_idx" ON "order"."refund_dispute" ("account_id");
CREATE INDEX IF NOT EXISTS "refund_dispute_status_idx" ON "order"."refund_dispute" ("status");

-- Order cancellation predicate. Three nullable status columns combine into one
-- boolean: any of confirm/transport/payout in ('Failed', 'Cancelled') means
-- the order is cancelled. NULL inputs (e.g. payout session not yet created,
-- transport row pre-shipment) coerce to FALSE so a missing leg never makes
-- an active order look cancelled. Used by ListBuyer{Pending,Completed,Cancelled}Orders.
CREATE OR REPLACE FUNCTION "order".is_cancelled(
    confirm_status   "order"."status",
    transport_status "order"."status",
    payout_status    "order"."status"
) RETURNS BOOLEAN
LANGUAGE SQL IMMUTABLE
AS $$
    SELECT COALESCE(confirm_status   IN ('Failed', 'Cancelled'), FALSE)
        OR COALESCE(transport_status IN ('Failed', 'Cancelled'), FALSE)
        OR COALESCE(payout_status    IN ('Failed', 'Cancelled'), FALSE);
$$;
