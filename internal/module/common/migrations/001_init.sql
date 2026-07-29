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
    "checksum" TEXT, -- Optional content hash
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    -- Soft delete, because "provider" + "object_key" is the only handle on the stored
    -- object: the row has to outlive the delete request until a reaper has removed the
    -- object from the backend. Deleting the row first leaks the file permanently, with
    -- no way to find it again short of diffing the whole bucket against this table.
    "deleted_at" TIMESTAMPTZ,

    CONSTRAINT "resource_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "resource_provider_object_key_key" UNIQUE ("provider", "object_key"),
    CONSTRAINT "resource_provider_format" CHECK ("provider" ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    CONSTRAINT "resource_size_non_negative" CHECK ("size" >= 0)
);
-- The reaper's queue: objects still to be removed from the storage backend.
CREATE INDEX IF NOT EXISTS "resource_pending_deletion_idx"
    ON "resource" ("deleted_at")
    WHERE "deleted_at" IS NOT NULL;
-- An account's uploads: storage quota, and erasure requests.
CREATE INDEX IF NOT EXISTS "resource_uploaded_by_id_idx" ON "resource" ("uploaded_by_id");
-- Deduplication: the same bytes uploaded by several sellers can reuse one object.
CREATE INDEX IF NOT EXISTS "resource_checksum_idx"
    ON "resource" ("checksum")
    WHERE "checksum" IS NOT NULL;

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

    CONSTRAINT "option_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "option_id_format" CHECK ("id" ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    CONSTRAINT "option_type_format" CHECK ("type" ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    CONSTRAINT "option_provider_format" CHECK ("provider" ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),

    CONSTRAINT "option_logo_resource_id_fkey" FOREIGN KEY ("logo_resource_id")
        REFERENCES "resource" ("id") ON DELETE SET NULL
);
-- Checkout: the enabled options of one type, in display order.
CREATE INDEX IF NOT EXISTS "option_enabled_type_priority_idx"
    ON "option" ("type", "priority")
    WHERE "is_enabled";
-- A seller's own options.
CREATE INDEX IF NOT EXISTS "option_owner_id_idx" ON "option" ("owner_id");
