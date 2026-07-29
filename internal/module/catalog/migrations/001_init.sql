-- Module: catalog — canonical schema
-- Description: Product catalog including categories, SPUs (Standard
--              Product Units), SKUs (Stock Keeping Units), tags, stock levels,
--              and search/vector sync state.
-- public, not the module schema: an extension belongs to one schema per database,
-- so leaving it here would hide the vector type from every other module.
CREATE EXTENSION IF NOT EXISTS vector
WITH
  SCHEMA public;

CREATE EXTENSION IF NOT EXISTS unaccent
WITH
  SCHEMA public;

-- accent-insensitive search
CREATE EXTENSION IF NOT EXISTS pg_trgm
WITH
  SCHEMA public;

-- trigram similarity
-- Accent-stripping normalizer for diacritic-insensitive search. unaccent() alone
-- is STABLE (resolves its dictionary via search_path) so it can't index; pinning
-- the dictionary makes this IMMUTABLE. translate() handles đ/Đ, which
-- unaccent.rules omits.
CREATE FUNCTION "f_unaccent"(text) RETURNS text
    LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT
    AS $$ SELECT translate(public.unaccent('public.unaccent', $1), 'đĐ', 'dD') $$;

-- Enums
-- Listing lifecycle + moderation state
CREATE TYPE "listing_status" AS ENUM ('draft', 'pending', 'active', 'hidden');

-- Item condition for C2C used goods (listing-level)
CREATE TYPE "listing_condition" AS ENUM ('new', 'used', 'damaged');

-- Pricing mode: fixed price or negotiable (offer)
CREATE TYPE "price_mode" AS ENUM ('fixed', 'negotiable');

-- Table
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

-- Hierarchical product category tree. parent_id = NULL means root category.
-- Declared before "product_spu" because that table FKs it.
CREATE TABLE
  IF NOT EXISTS "category" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "parent_id" BIGINT, -- NULL = root category; else FK to parent category
    "name" VARCHAR(100) NOT NULL,
    "description" TEXT NOT NULL,
    "stale_at" TIMESTAMPTZ, -- NULL = fresh

    CONSTRAINT "category_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "category_name_key" UNIQUE ("name"),
    -- Deleting a parent promotes its children to roots.
    CONSTRAINT "category_parent_id_fkey" FOREIGN KEY ("parent_id") REFERENCES "category" ("id") ON DELETE SET NULL
  );

CREATE INDEX IF NOT EXISTS "category_parent_id_idx" ON "category" ("parent_id");

-- SPU (Standard Product Unit): the canonical product definition shared across all sellers.
-- cached_rating is denormalized from trust.review and maintained by the catalog
-- service: trust is another schema, so it cannot be joined. Price is not cached —
-- see product_sku_price_idx.
CREATE TABLE
  IF NOT EXISTS "product_spu" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "slug" VARCHAR(100) NOT NULL, -- URL-friendly slug derived from name, must be globally unique
    "account_id" BIGINT NOT NULL, -- Seller account that owns this listing
    "category_id" BIGINT NOT NULL,
    -- The variant shown in search results and the product card. Nullable so an SPU
    -- can be inserted before its SKUs (see product_featured_sku_id_fkey).
    "featured_sku_id" BIGINT,
    -- Core product info
    "status" "listing_status" NOT NULL DEFAULT 'draft', -- lifecycle + moderation state
    "name" TEXT NOT NULL,
    "description" TEXT NOT NULL,
    "specifications" JSONB NOT NULL, -- Structured attribute schema specific to the product type
    -- Gallery, ordered: the first id is the cover. Resource ids owned by the common
    -- module, held inline because a listing and its images sit in two schemas and
    -- writing both atomically stops being possible once the modules are split.
    "attachments" BIGINT[] NOT NULL DEFAULT '{}',
    -- Pricing
    "price_mode" "price_mode" NOT NULL, -- fixed price vs negotiable (offer)
    "condition" "listing_condition" NOT NULL, -- item condition (C2C used goods), listing-level
    "currency" VARCHAR(3) NOT NULL, -- ISO 4217 currency code for all SKU prices under this SPU

    -- Edits
    "pending_edits" JSONB NOT NULL DEFAULT '{}', -- Optional pending edits to be applied after moderation approval
    -- Denormalized
    "cached_rating" DOUBLE PRECISION NOT NULL DEFAULT 0, -- average trust.review rating (1..5), 0 = no reviews yet

    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, -- sorts the "newest listings" feed
    -- Soft delete, because order.item holds spu_id / sku_id without an FK and order
    -- history must stay resolvable. Distinct from status='hidden', which is a live
    -- listing the seller took down temporarily.
    "deleted_at" TIMESTAMPTZ,
    "stale_at" TIMESTAMPTZ, -- NULL = fresh

    CONSTRAINT "product_spu_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "product_spu_slug_key" UNIQUE ("slug"),
    CONSTRAINT "product_spu_featured_sku_id_key" UNIQUE ("featured_sku_id"),
    CONSTRAINT "product_spu_currency_format" CHECK ("currency" ~ '^[A-Z]{3}$'),
    -- RESTRICT: a category must be emptied before it can be deleted ("category_id"
    -- is NOT NULL, so SET NULL is not an option).
    CONSTRAINT "product_spu_category_id_fkey" FOREIGN KEY ("category_id") REFERENCES "category" ("id") ON DELETE RESTRICT
  );

CREATE INDEX IF NOT EXISTS "product_spu_account_id_idx" ON "product_spu" ("account_id");

CREATE INDEX IF NOT EXISTS "product_spu_category_id_idx" ON "product_spu" ("category_id");

CREATE INDEX IF NOT EXISTS "product_spu_name_unaccent_trgm_idx" ON "product_spu" USING gin ("f_unaccent" ("name") gin_trgm_ops);

-- Browse feeds, one index per sort key. Partial on live active rows, which is all a
-- buyer sees; that also covers filtering by "status", too low-cardinality to index on
-- its own. Sorting by price goes through product_sku_price_idx instead.
CREATE INDEX IF NOT EXISTS "product_spu_active_created_at_idx"
    ON "product_spu" ("created_at" DESC)
    WHERE "status" = 'active' AND "deleted_at" IS NULL;
CREATE INDEX IF NOT EXISTS "product_spu_active_cached_rating_idx"
    ON "product_spu" ("cached_rating" DESC)
    WHERE "status" = 'active' AND "deleted_at" IS NULL;
-- The moderation queue.
CREATE INDEX IF NOT EXISTS "product_spu_pending_created_at_idx"
    ON "product_spu" ("created_at")
    WHERE "status" = 'pending' AND "deleted_at" IS NULL;

-- SKU (Stock Keeping Unit): a specific purchasable variant of an SPU (e.g. size=l, color=red).
CREATE TABLE
  IF NOT EXISTS "product_sku" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "spu_id" BIGINT NOT NULL,
    "price" BIGINT NOT NULL, -- Price in smallest currency unit (e.g. VND, cents)
    "attributes" JSONB NOT NULL, -- Variant attribute key/value pairs (e.g. {"size": "l", "color": "red"})
    "package_details" JSONB NOT NULL, -- Physical packaging info for shipping (weight, dimensions, etc.)
    -- Variant-specific images; empty means fall back to the SPU gallery.
    "attachments" BIGINT[] NOT NULL DEFAULT '{}',

    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "deleted_at" TIMESTAMPTZ, -- same soft-delete rule as product_spu
    CONSTRAINT "product_sku_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "product_price_positive_check" CHECK ("price" >= 0),
    CONSTRAINT "product_sku_spu_id_fkey" FOREIGN KEY ("spu_id") REFERENCES "product_spu" ("id") ON DELETE CASCADE
  );

CREATE INDEX IF NOT EXISTS "product_sku_spu_id_idx" ON "product_sku" ("spu_id");
-- The ordered side of the price-sorted feed: scanned in price order and nested-looped
-- into product_spu, so rows come out already sorted and LIMIT stops early.
CREATE INDEX IF NOT EXISTS "product_sku_price_idx"
    ON "product_sku" ("price")
    WHERE "deleted_at" IS NULL;
-- product_spu and product_sku reference each other, so this FK cannot be inlined in
-- either table. Insert order: the SPU with a NULL featured_sku_id, its SKUs, then
-- UPDATE the SPU to point at one.
ALTER TABLE "product_spu" ADD CONSTRAINT "product_featured_sku_id_fkey" FOREIGN KEY ("featured_sku_id") REFERENCES "product_sku" ("id") ON DELETE SET NULL;


-- Flat tag dictionary. id is the tag slug (e.g. 'eco-friendly', 'handmade').
CREATE TABLE
  IF NOT EXISTS "tag" (
    "id" VARCHAR(100) NOT NULL,
    "description" VARCHAR(255),
    "stale_at" TIMESTAMPTZ, -- NULL = fresh
    CONSTRAINT "tag_pkey" PRIMARY KEY ("id")
  );

-- Many-to-many join between SPUs and tags.
CREATE TABLE
  IF NOT EXISTS "product_spu_tag" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "spu_id" BIGINT NOT NULL,
    "tag" VARCHAR(100) NOT NULL,
    CONSTRAINT "product_spu_tag_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "product_spu_tag_spu_id_tag_key" UNIQUE ("spu_id", "tag"),
    CONSTRAINT "product_spu_tag_spu_id_fkey" FOREIGN KEY ("spu_id") REFERENCES "product_spu" ("id") ON DELETE CASCADE,
    CONSTRAINT "product_spu_tag_tag_fkey" FOREIGN KEY ("tag") REFERENCES "tag" ("id") ON DELETE CASCADE ON UPDATE CASCADE
  );

-- Vector search (pgvector), both vectors from BGE-M3. Rows are created by the sync cron
-- and stay NULL until the first embedding pass.
-- "sparse" has one non-zero per distinct input token and HNSW caps that at 1000, so the
-- embedding job must keep max_length at 512-1024. The cap is enforced by the index at
-- INSERT ("sparsevec cannot have more than 1000 non-zero elements"), not at build time,
-- so an over-long input fails the write rather than degrading search.
-- ip_ops for sparse (lexical matching is a dot product), cosine for dense.
CREATE TABLE
  IF NOT EXISTS "product_embedding" (
    "spu_id" BIGINT NOT NULL,
    "dense" vector (1024), -- dense vector; NULL until embedded
    "sparse" sparsevec (250048), -- sparse (lexical) vector; NULL until embedded. Max 1000 non-zeros
    "updated_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, -- last embedding pass, not an audit field
    CONSTRAINT "product_embedding_pkey" PRIMARY KEY ("spu_id"),
    CONSTRAINT "product_embedding_spu_id_fkey" FOREIGN KEY ("spu_id") REFERENCES "product_spu" ("id") ON DELETE CASCADE
  );

CREATE INDEX IF NOT EXISTS "product_embedding_dense_idx" ON "product_embedding" USING hnsw ("dense" vector_cosine_ops);

CREATE INDEX IF NOT EXISTS "product_embedding_sparse_idx" ON "product_embedding" USING hnsw ("sparse" sparsevec_ip_ops);

-- Stores dense + sparse embeddings per category.
-- Rows are created by the sync cron; vectors remain NULL until the first embedding pass.
CREATE TABLE
  IF NOT EXISTS "category_embedding" (
    "category_id" BIGINT NOT NULL,
    "dense" vector (1024), -- dense vector; NULL until embedded
    "sparse" sparsevec (250048), -- sparse (lexical) vector; NULL until embedded. Max 1000 non-zeros
    "updated_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, -- last embedding pass, not an audit field
    CONSTRAINT "category_embedding_pkey" PRIMARY KEY ("category_id"),
    CONSTRAINT "category_embedding_category_id_fkey" FOREIGN KEY ("category_id") REFERENCES "category" ("id") ON DELETE CASCADE
  );

CREATE INDEX IF NOT EXISTS "category_embedding_dense_idx" ON "category_embedding" USING hnsw ("dense" vector_cosine_ops);

CREATE INDEX IF NOT EXISTS "category_embedding_sparse_idx" ON "category_embedding" USING hnsw ("sparse" sparsevec_ip_ops);

-- Stores dense + sparse embeddings per tag.
-- Rows are created by the sync cron; vectors remain NULL until the first embedding pass.
CREATE TABLE
  IF NOT EXISTS "tag_embedding" (
    "tag_id" VARCHAR(100) NOT NULL,
    "dense" vector (1024), -- dense vector; NULL until embedded
    "sparse" sparsevec (250048), -- sparse (lexical) vector; NULL until embedded. Max 1000 non-zeros
    "updated_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, -- last embedding pass, not an audit field
    CONSTRAINT "tag_embedding_pkey" PRIMARY KEY ("tag_id"),
    CONSTRAINT "tag_embedding_tag_id_fkey" FOREIGN KEY ("tag_id") REFERENCES "tag" ("id") ON DELETE CASCADE ON UPDATE CASCADE
  );

CREATE INDEX IF NOT EXISTS "tag_embedding_dense_idx" ON "tag_embedding" USING hnsw ("dense" vector_cosine_ops);

CREATE INDEX IF NOT EXISTS "tag_embedding_sparse_idx" ON "tag_embedding" USING hnsw ("sparse" sparsevec_ip_ops);

-- Per-account interest slots, read by PK to build a feed: the ANN search runs against
-- product_embedding, not here, and a few slots per account cost nothing to scan. No vector
-- index — it would only serve the reverse lookup, and absorb constant rewrites.
CREATE TABLE
  IF NOT EXISTS "account_interest" (
    "account_id" BIGINT NOT NULL, -- cross-module ref to account module; FK intentionally not declared
    "slot" SMALLINT NOT NULL, -- 1..NumInterests
    "dense" vector (1024) NOT NULL,
    "strength" REAL NOT NULL DEFAULT 0,
    "updated_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, -- last recompute, not an audit field
    CONSTRAINT "account_interest_pkey" PRIMARY KEY ("account_id", "slot")
  );

-- Stock level record for a product SKU.
CREATE TABLE IF NOT EXISTS "stock" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "sku_id" BIGINT NOT NULL, -- ID of the owning SKU
    "stock" BIGINT NOT NULL, -- Total quantity in stock
    "taken" BIGINT NOT NULL DEFAULT 0, -- Quantity currently reserved or sold
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "stock_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "stock_sku_id_key" UNIQUE ("sku_id"),
    -- Without the last one an oversold row is a legal row.
    CONSTRAINT "stock_non_negative" CHECK ("stock" >= 0),
    CONSTRAINT "stock_taken_non_negative" CHECK ("taken" >= 0),
    CONSTRAINT "stock_taken_within_stock" CHECK ("taken" <= "stock"),

    CONSTRAINT "stock_sku_id_fkey" FOREIGN KEY ("sku_id")
        REFERENCES "product_sku" ("id") ON DELETE CASCADE
);
-- Serves ListMostTakenSku: ORDER BY taken DESC, range scan stops at LIMIT.
CREATE INDEX IF NOT EXISTS "stock_taken_idx" ON "stock" ("taken" DESC);
