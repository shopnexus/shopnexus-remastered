-- Shared DDL: applied into every module's schema. Each module owns the options of the kind
-- it acts on — finance the payment rails, order the carriers, account the notification
-- channels — so the registry is not one table every module writes.
CREATE TABLE IF NOT EXISTS "option" (
    "id" VARCHAR(100) NOT NULL, -- Stable kebab-case identifier (e.g. 'stripe-main', 'vnpay-qr', 'ghn-express')
    "owner_id" BIGINT, -- Account that created this option; NULL for system-provided options
    "is_enabled" BOOLEAN NOT NULL,
    "name" TEXT NOT NULL,
    "description" TEXT NOT NULL,
    -- Display order. Ties are possible, so a query that wants a stable order has to
    -- add "id" as a tiebreaker.
    "priority" INTEGER NOT NULL,
    "logo_resource_id" BIGINT,
    -- Non-secret configuration only: endpoints, supported currencies, feature flags.
    "data" JSONB NOT NULL DEFAULT '{}',
    -- Vault path holding this option's credentials, e.g. 'payment/stripe/main'. The
    -- provider client resolves it at runtime, so no key material is in this database,
    -- its backups, its replicas, or an "audit_log" snapshot of this row.
    "vault_secret_path" TEXT,

    -- Grouping
    "type" TEXT NOT NULL, -- High-level grouping key, kebab-case (e.g. 'payment', 'transport', 'notification')
    "provider" TEXT NOT NULL, -- Sub-grouping key, kebab-case (e.g. 'stripe', 'vnpay', 'ghn')

    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    -- Soft delete, for the same reason the slug is immutable: order.item.transport_option
    -- and finance.transaction.payment_option hold it as a plain string with no foreign
    -- key, so a hard delete would leave every past order and every settled payment naming
    -- a carrier or a rail that can no longer be resolved. Retiring one is
    -- "is_enabled" = false; this is for removing it from the registry outright.
    "deleted_at" TIMESTAMPTZ,

    CONSTRAINT "option_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "option_id_format" CHECK ("id" ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    CONSTRAINT "option_type_format" CHECK ("type" ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    CONSTRAINT "option_provider_format" CHECK ("provider" ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),

    CONSTRAINT "option_logo_resource_id_fkey" FOREIGN KEY ("logo_resource_id")
        REFERENCES "resource" ("id") ON DELETE SET NULL
);
-- Checkout: the live options of one type, in display order.
CREATE INDEX IF NOT EXISTS "option_enabled_type_priority_idx"
    ON "option" ("type", "priority")
    WHERE "is_enabled" AND "deleted_at" IS NULL;
-- A seller's own options.
CREATE INDEX IF NOT EXISTS "option_owner_id_idx" ON "option" ("owner_id");
