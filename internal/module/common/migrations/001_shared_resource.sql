-- Shared DDL: applied into every module's schema by cmd/migrate, so a module owns the
-- rows it uploaded and can move to its own database with them. In dev every DSN points at
-- the same server, so the tables sit side by side in one place.
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
