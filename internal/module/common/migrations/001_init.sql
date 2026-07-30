-- Module: common — canonical schema
-- Description: Cross-module shared infrastructure: file/media resources and the
--              pluggable service option registry (payment providers, transport
--              providers, etc.).
--              Owning rows point at a resource from their own "attachments" array;
--              there is no join table, so nothing at the database level enforces that
--              an attached resource still exists — the owning module has to check.

-- Tables

CREATE TABLE IF NOT EXISTS "audit_log" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "version" BIGINT NOT NULL DEFAULT 1, -- Incremented on each change to the same record
    "table_name" VARCHAR(100) NOT NULL,
    -- TEXT because this schema mixes key types: "resource" is BIGINT, "option" is a
    -- VARCHAR slug.
    "record_id" TEXT NOT NULL,
    "change_type" VARCHAR(10) NOT NULL, -- 'insert', 'update', 'delete'
    "code" VARCHAR(100) NOT NULL, -- e.g. Business code 'option.enable', 'option.rotate-secret'
    "changed_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "changed_by" BIGINT, -- account_id of the user who made the change (if applicable)
    "diff" JSONB NOT NULL, -- JSON diff of the record's fields (for insert only, other diff = snapshot)
    "snapshot" JSONB NOT NULL, -- Full record values after the change
    CONSTRAINT "audit_log_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "audit_log_table_name_record_id_version_key" UNIQUE ("table_name", "record_id", "version")
);

-- Uploaded file/media record. provider identifies the storage backend.
CREATE TABLE IF NOT EXISTS "resource" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "uploaded_by_id" BIGINT, -- Account that uploaded the file; NULL for system-generated resources
    "provider" TEXT NOT NULL, -- Storage backend identifier, kebab-case (e.g. 's3', 'minio', 'local')
    "object_key" VARCHAR(2048) NOT NULL, -- Provider-specific path or key (up to 2048 chars for S3 compatibility)
    "mime" VARCHAR(100) NOT NULL, -- MIME type (e.g. 'image/jpeg', 'application/pdf')
    "size" BIGINT NOT NULL, -- File size in bytes
    "metadata" JSONB NOT NULL DEFAULT '{}', -- Provider-specific metadata (dimensions, duration, CDN URL, etc.)
    -- Content hash, and only ever the one read back from storage at completion. A hash
    -- the client merely claimed is used to look for an existing row and then thrown
    -- away: persisting it would let anyone reserve a slot, declare the hash of bytes
    -- they do not have, and have the next honest upload of those bytes deduplicated
    -- onto their object. "resource_checksum_needs_completion" is that rule in the
    -- schema rather than only in the service.
    "checksum" TEXT,
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    -- Set when the upload was confirmed and the object was read back. Until then the row
    -- is a reservation: there may be no object at "object_key" at all, which is why an
    -- unconfirmed resource may not be attached to anything.
    "completed_at" TIMESTAMPTZ,
    -- Soft delete, because "provider" + "object_key" is the only handle on the stored
    -- object: the row has to outlive the delete request until a reaper has removed the
    -- object from the backend. Deleting the row first leaks the file permanently, with
    -- no way to find it again short of diffing the whole bucket against this table.
    "deleted_at" TIMESTAMPTZ,

    CONSTRAINT "resource_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "resource_provider_object_key_key" UNIQUE ("provider", "object_key"),
    CONSTRAINT "resource_provider_format" CHECK ("provider" ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    CONSTRAINT "resource_size_non_negative" CHECK ("size" >= 0),
    CONSTRAINT "resource_checksum_needs_completion" CHECK (
        "checksum" IS NULL OR "completed_at" IS NOT NULL
    )
);
-- The reaper's queue: objects still to be removed from the storage backend.
CREATE INDEX IF NOT EXISTS "resource_pending_deletion_idx"
    ON "resource" ("deleted_at")
    WHERE "deleted_at" IS NOT NULL;
-- Abandoned reservations: a slot was taken and the bytes never arrived. Same shape as
-- draft_order_expiring_idx — without it the only way to find them is a full scan, so
-- they would accumulate forever.
CREATE INDEX IF NOT EXISTS "resource_abandoned_idx"
    ON "resource" ("created_at")
    WHERE "completed_at" IS NULL AND "deleted_at" IS NULL;
-- An account's uploads. Two readers, neither of them an endpoint: the quota check when a
-- slot is reserved, and erasure when an account is deleted.
CREATE INDEX IF NOT EXISTS "resource_uploaded_by_id_idx" ON "resource" ("uploaded_by_id");
-- Deduplication, per uploader. Not marketplace-wide: one row has one "uploaded_by_id"
-- and no reference count, so two accounts sharing an object means either of them can
-- delete it out from under the other's listing. Re-uploading identical bytes is rare
-- enough between accounts — and usually means a stolen photo — that the storage saved is
-- not worth a deletion that takes somebody else's image down.
CREATE INDEX IF NOT EXISTS "resource_uploader_checksum_idx"
    ON "resource" ("uploaded_by_id", "checksum")
    WHERE "checksum" IS NOT NULL AND "deleted_at" IS NULL;

-- Registry of pluggable service integrations selectable at checkout or configuration time.
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
