-- =============================================
-- Module: Catalog
-- Schema: catalog
-- Description: Product catalog including categories, SPUs (Standard
--              Product Units), SKUs (Stock Keeping Units), tags, comments,
--              and search/vector sync state.
-- =============================================

CREATE SCHEMA IF NOT EXISTS "catalog";

CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS unaccent WITH SCHEMA public; -- accent-insensitive search
CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;  -- trigram similarity

-- Accent-stripping normalizer for diacritic-insensitive search. unaccent() alone
-- is STABLE (resolves its dictionary via search_path) so it can't index; pinning
-- the dictionary makes this IMMUTABLE. translate() handles đ/Đ, which
-- unaccent.rules omits.
CREATE FUNCTION "catalog"."f_unaccent"(text) RETURNS text
    LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT
    AS $$ SELECT translate(public.unaccent('public.unaccent', $1), 'đĐ', 'dD') $$;

-- Enums

-- Entities a comment can be attached to (product or another comment for threading)
CREATE TYPE "catalog"."comment_ref_type" AS ENUM ('ProductSpu', 'Comment');
-- Entities tracked in the search sync queue
CREATE TYPE "catalog"."search_sync_ref_type" AS ENUM ('ProductSpu', 'Category', 'Tag');

-- Tables

-- Hierarchical product category tree. parent_id = NULL means root category.
CREATE TABLE IF NOT EXISTS "catalog"."category" (
    "id" UUID NOT NULL DEFAULT gen_random_uuid(),
    "name" VARCHAR(100) NOT NULL,
    "description" TEXT NOT NULL,
    "parent_id" UUID, -- NULL for top-level categories

    CONSTRAINT "category_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "category_name_key" UNIQUE ("name")
);
CREATE INDEX IF NOT EXISTS "category_parent_id_idx" ON "catalog"."category" ("parent_id");

-- SPU (Standard Product Unit): the canonical product definition shared across all sellers
CREATE TABLE IF NOT EXISTS "catalog"."product_spu" (
    "id" UUID NOT NULL DEFAULT gen_random_uuid(),
    "number" BIGINT NOT NULL GENERATED ALWAYS AS IDENTITY, -- Human-readable sequential listing number
    "slug" TEXT NOT NULL, -- URL-friendly slug derived from name, must be globally unique
    "account_id" UUID NOT NULL, -- Seller account that owns this listing
    "category_id" UUID NOT NULL,
    "featured_sku_id" UUID, -- The variant displayed in search results and the product card
    "name" TEXT NOT NULL,
    "description" TEXT NOT NULL,
    "is_enabled" BOOLEAN NOT NULL,
    "currency" VARCHAR(3) NOT NULL, -- ISO 4217 currency code for all SKU prices under this SPU
    "specifications" JSONB NOT NULL, -- Structured attribute schema specific to the product type
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "date_updated" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "date_deleted" TIMESTAMPTZ(3), -- Soft-delete timestamp; NULL means the product is live

    CONSTRAINT "product_spu_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "product_spu_slug_key" UNIQUE ("slug"),
    CONSTRAINT "product_spu_featured_sku_id_key" UNIQUE ("featured_sku_id"),

    CONSTRAINT "product_spu_category_id_fkey" FOREIGN KEY ("category_id")
        REFERENCES "catalog"."category" ("id") ON DELETE CASCADE ON UPDATE CASCADE
);
CREATE INDEX IF NOT EXISTS "product_spu_account_id_idx" ON "catalog"."product_spu" ("account_id");
CREATE INDEX IF NOT EXISTS "product_spu_category_id_idx" ON "catalog"."product_spu" ("category_id");
CREATE INDEX IF NOT EXISTS "product_spu_name_unaccent_trgm_idx"
    ON "catalog"."product_spu" USING gin ("catalog"."f_unaccent"("name") gin_trgm_ops);

-- SKU (Stock Keeping Unit): a specific purchasable variant of an SPU (e.g. size=L, color=Red).
CREATE TABLE IF NOT EXISTS "catalog"."product_sku" (
    "id" UUID NOT NULL DEFAULT gen_random_uuid(),
    "spu_id" UUID NOT NULL,
    "price" BIGINT NOT NULL, -- Price in smallest currency unit (e.g. VND, cents)
    "shared_packaging" BOOLEAN NOT NULL, -- FALSE = each unit requires its own package (e.g. serialized/large items) else multiple units can share a single package
    "attributes" JSONB NOT NULL, -- Variant attribute key/value pairs (e.g. {"size": "L", "color": "Red"})
    "package_details" JSONB NOT NULL, -- Physical packaging info for shipping (weight, dimensions, etc.)
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "date_deleted" TIMESTAMPTZ(3), -- Soft-delete timestamp; NULL means the SKU is purchasable

    CONSTRAINT "product_sku_pkey" PRIMARY KEY ("id"),

    CONSTRAINT "product_sku_spu_id_fkey" FOREIGN KEY ("spu_id")
        REFERENCES "catalog"."product_spu" ("id") ON DELETE CASCADE ON UPDATE CASCADE
);
CREATE INDEX IF NOT EXISTS "product_sku_spu_id_idx" ON "catalog"."product_sku" ("spu_id");

-- Cross-table FK from product_spu.featured_sku_id, deferred because
-- product_sku also FKs back to product_spu (circular).
ALTER TABLE "catalog"."product_spu"
    ADD CONSTRAINT "product_featured_sku_id_fkey" FOREIGN KEY ("featured_sku_id")
        REFERENCES "catalog"."product_sku" ("id") ON DELETE SET NULL ON UPDATE CASCADE;

-- Flat tag dictionary. id is the tag slug (e.g. 'eco-friendly', 'handmade').
CREATE TABLE IF NOT EXISTS "catalog"."tag" (
    "id" VARCHAR(100) NOT NULL,
    "name" VARCHAR(100) NOT NULL,
    "description" VARCHAR(255),

    CONSTRAINT "tag_pkey" PRIMARY KEY ("id")
);

-- Many-to-many join between SPUs and tags.
CREATE TABLE IF NOT EXISTS "catalog"."product_spu_tag" (
    "id" BIGSERIAL NOT NULL,
    "spu_id" UUID NOT NULL,
    "tag" VARCHAR(100) NOT NULL,

    CONSTRAINT "product_spu_tag_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "product_spu_tag_spu_id_tag_key" UNIQUE ("spu_id", "tag"),

    CONSTRAINT "product_spu_tag_spu_id_fkey" FOREIGN KEY ("spu_id")
        REFERENCES "catalog"."product_spu" ("id") ON DELETE CASCADE ON UPDATE CASCADE,
    CONSTRAINT "product_spu_tag_tag_fkey" FOREIGN KEY ("tag")
        REFERENCES "catalog"."tag" ("id") ON DELETE CASCADE ON UPDATE CASCADE
);

-- User comments on products or replies to other comments (threaded via ref_type/ref_id).
-- score is a derived ranking value (e.g. Wilson score) from upvote/downvote counts.
CREATE TABLE IF NOT EXISTS "catalog"."comment" (
    "id" UUID NOT NULL DEFAULT gen_random_uuid(),
    "account_id" UUID NOT NULL,
    "order_id" UUID, -- Optional reference to the order that purchased the product being reviewed (must not null when ref_type='ProductSpu')
    "ref_type" "catalog"."comment_ref_type" NOT NULL, -- 'ProductSpu' = top-level review; 'Comment' = reply to another comment
    "ref_id" UUID NOT NULL,
    "body" TEXT NOT NULL,
    "upvote" BIGINT NOT NULL DEFAULT 0,
    "downvote" BIGINT NOT NULL DEFAULT 0,
    "score" DOUBLE PRECISION NOT NULL, -- Computed ranking score (e.g. upvote - downvote or Wilson lower bound)
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "date_updated" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "comment_pkey" PRIMARY KEY ("id")
);

-- Sync queue for the vector search index. A background cron polls this table
-- for stale entries and re-embeds them. Scalar filters read product tables directly.
CREATE TABLE IF NOT EXISTS "catalog"."search_sync" (
    "id" BIGSERIAL NOT NULL,
    "ref_type" "catalog"."search_sync_ref_type" NOT NULL,
    "ref_id" UUID NOT NULL,
    "is_stale_embedding" BOOLEAN NOT NULL DEFAULT true, -- True when the product text has changed and the embedding must be regenerated
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "date_updated" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "search_sync_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "search_sync_ref_type_ref_id_key" UNIQUE ("ref_type", "ref_id")
);
CREATE INDEX IF NOT EXISTS "search_sync_is_stale_embedding_idx" ON "catalog"."search_sync" ("is_stale_embedding");
CREATE INDEX IF NOT EXISTS "search_sync_date_created_idx" ON "catalog"."search_sync" ("date_created");

-- Vector search (pgvector). Dims are tied to the embedding model (MGTE: dense 768,
-- XLM-R vocab sparse). Switching models (e.g. BGE-M3 dense 1024) = ALTER COLUMN
-- TYPE + mark all search_sync rows stale so the cron re-embeds everything.

-- Stores dense + sparse embeddings per SPU.
-- Rows are created by the sync cron; vectors remain NULL until the first embedding pass.
CREATE TABLE IF NOT EXISTS "catalog"."product_embedding" (
    "spu_id" UUID NOT NULL,
    "embedding" vector(768), -- MGTE dense vector; NULL until embedded
    "sparse" sparsevec(250048), -- MGTE sparse (lexical) vector; NULL until embedded
    "date_updated" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "product_embedding_pkey" PRIMARY KEY ("spu_id"),

    CONSTRAINT "product_embedding_spu_id_fkey" FOREIGN KEY ("spu_id")
        REFERENCES "catalog"."product_spu" ("id") ON DELETE CASCADE ON UPDATE CASCADE
);
CREATE INDEX IF NOT EXISTS "product_embedding_embedding_idx" ON "catalog"."product_embedding" USING hnsw ("embedding" vector_cosine_ops);
CREATE INDEX IF NOT EXISTS "product_embedding_sparse_idx" ON "catalog"."product_embedding" USING hnsw ("sparse" sparsevec_ip_ops);

-- Stores dense + sparse embeddings per category.
-- Rows are created by the sync cron; vectors remain NULL until the first embedding pass.
CREATE TABLE IF NOT EXISTS "catalog"."category_embedding" (
    "category_id" UUID NOT NULL,
    "embedding" vector(768), -- MGTE dense vector; NULL until embedded
    "sparse" sparsevec(250048), -- MGTE sparse (lexical) vector; NULL until embedded
    "date_updated" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "category_embedding_pkey" PRIMARY KEY ("category_id"),

    CONSTRAINT "category_embedding_category_id_fkey" FOREIGN KEY ("category_id")
        REFERENCES "catalog"."category" ("id") ON DELETE CASCADE ON UPDATE CASCADE
);
CREATE INDEX IF NOT EXISTS "category_embedding_embedding_idx" ON "catalog"."category_embedding" USING hnsw ("embedding" vector_cosine_ops);
CREATE INDEX IF NOT EXISTS "category_embedding_sparse_idx" ON "catalog"."category_embedding" USING hnsw ("sparse" sparsevec_ip_ops);

-- Stores dense + sparse embeddings per tag.
-- Rows are created by the sync cron; vectors remain NULL until the first embedding pass.
CREATE TABLE IF NOT EXISTS "catalog"."tag_embedding" (
    "tag_id" VARCHAR(100) NOT NULL,
    "embedding" vector(768), -- MGTE dense vector; NULL until embedded
    "sparse" sparsevec(250048), -- MGTE sparse (lexical) vector; NULL until embedded
    "date_updated" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "tag_embedding_pkey" PRIMARY KEY ("tag_id"),

    CONSTRAINT "tag_embedding_tag_id_fkey" FOREIGN KEY ("tag_id")
        REFERENCES "catalog"."tag" ("id") ON DELETE CASCADE ON UPDATE CASCADE
);
CREATE INDEX IF NOT EXISTS "tag_embedding_embedding_idx" ON "catalog"."tag_embedding" USING hnsw ("embedding" vector_cosine_ops);
CREATE INDEX IF NOT EXISTS "tag_embedding_sparse_idx" ON "catalog"."tag_embedding" USING hnsw ("sparse" sparsevec_ip_ops);

-- Per-account interest slots.
-- Always fetched by PK, never ANN-searched — no vector index needed.
CREATE TABLE IF NOT EXISTS "catalog"."account_interest" (
    "account_id" UUID NOT NULL, -- cross-module ref to account module; FK intentionally not declared
    "slot" SMALLINT NOT NULL, -- 1..NumInterests (catalogutil)
    "embedding" vector(768) NOT NULL,
    "strength" REAL NOT NULL DEFAULT 0,
    "date_updated" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "account_interest_pkey" PRIMARY KEY ("account_id", "slot")
);
