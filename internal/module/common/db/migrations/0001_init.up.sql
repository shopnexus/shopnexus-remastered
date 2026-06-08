-- =============================================
-- Module: Common
-- Schema: common
-- Description: Cross-module shared infrastructure: file/media resources,
--              resource-to-entity references, and pluggable service option registry
--              (payment providers, transport providers, etc.).
-- =============================================

CREATE SCHEMA IF NOT EXISTS "common";

-- Enums

-- Entity types that can have media attachments
CREATE TYPE "common"."resource_ref_type" AS ENUM ('ProductSpu', 'ProductSku', 'Refund', 'RefundDispute', 'Comment');

-- Tables

-- Uploaded file/media record. provider identifies the storage backend.
CREATE TABLE IF NOT EXISTS "common"."resource" (
    "id" UUID NOT NULL DEFAULT gen_random_uuid(),
    "uploaded_by_id" UUID, -- Account that uploaded the file; NULL for system-generated resources
    "provider" TEXT NOT NULL, -- Storage backend identifier (e.g. 'S3', 'Local', 'MinIO')
    "object_key" VARCHAR(2048) NOT NULL, -- Provider-specific path or key (up to 2048 chars for S3 compatibility)
    "mime" VARCHAR(100) NOT NULL, -- MIME type (e.g. 'image/jpeg', 'application/pdf')
    "size" BIGINT NOT NULL, -- File size in bytes
    "metadata" JSONB NOT NULL, -- Provider-specific metadata (dimensions, duration, CDN URL, etc.)
    "checksum" TEXT, -- Optional content hash
    "created_at" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "resource_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "resource_provider_object_key_key" UNIQUE ("provider", "object_key")
);

-- Associates a resource (file) with a domain entity.
-- This indirection allows a single file to be referenced by multiple entities.
CREATE TABLE IF NOT EXISTS "common"."resource_reference" (
    "id" BIGSERIAL NOT NULL,
    "rs_id" UUID NOT NULL, -- The resource being referenced
    "ref_type" "common"."resource_ref_type" NOT NULL,
    "ref_id" UUID NOT NULL, -- ID of the owning entity (product, comment, etc.)
    "order" INTEGER NOT NULL, -- Display order position among other resources for the same entity

    CONSTRAINT "resource_reference_pkey" PRIMARY KEY ("id"),

    CONSTRAINT "resource_reference_rs_id_fkey" FOREIGN KEY ("rs_id")
        REFERENCES "common"."resource" ("id") ON DELETE CASCADE ON UPDATE CASCADE
);

-- Registry of pluggable service integrations selectable at checkout or configuration time.
CREATE TABLE IF NOT EXISTS "common"."option" (
    "id" VARCHAR(100) NOT NULL, -- Stable identifier for this (e.g. 'stripe-xxx', 'vnpay-qr|bank|xxx', 'ghn-xxx')
    "owner_id" UUID, -- Account that created this option; NULL for system-provided options
    "is_enabled" BOOLEAN NOT NULL,
    "name" TEXT NOT NULL,
    "description" TEXT NOT NULL,
    "priority" INTEGER NOT NULL, -- Better UX if options are displayed in a consistent order
    "logo_rs_id" UUID,
    "data" JSONB NOT NULL, -- specific configuration data (e.g. API keys, supported features, etc.)

    -- Grouping
    "type" TEXT NOT NULL, -- High-level grouping key (e.g. 'payment', 'transport', 'notification', ...)
    "provider" TEXT NOT NULL, -- Sub-grouping key (e.g. 'stripe', 'vnpay', 'ghn', ...)

    CONSTRAINT "option_pkey" PRIMARY KEY ("id"),

    CONSTRAINT "option_logo_rs_id_fkey" FOREIGN KEY ("logo_rs_id")
        REFERENCES "common"."resource" ("id") ON DELETE SET NULL ON UPDATE CASCADE
);
CREATE INDEX IF NOT EXISTS "option_type_provider_idx" ON "common"."option" ("type", "provider");
