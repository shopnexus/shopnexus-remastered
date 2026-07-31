-- Module: catalog — canonical schema
-- Description: Listings and their purchasable variants, categories, tags, stock
--              levels, wishlists, and search/vector sync state. A listing is one
--              seller's offer, not an entry in a shared product master: the seller
--              id, the condition and the slug all sit on it.
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

-- Who pays the shipping fee, chosen by the seller at creation. Not derivable from
-- anything else on the row, and the checkout quote reads it.
CREATE TYPE "shipping_paid_by" AS ENUM ('buyer', 'seller');

-- Table
CREATE TABLE
  IF NOT EXISTS "audit_log" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "version" BIGINT NOT NULL DEFAULT 1, -- Incremented on each change to the same record
    "table_name" VARCHAR(100) NOT NULL,
    "record_id" BIGINT NOT NULL,
    "change_type" VARCHAR(10) NOT NULL, -- 'insert', 'update', 'delete'
    "code" VARCHAR(100) NOT NULL, -- e.g. Business code 'listing.publish', 'comment.delete', 'account.suspend'
    "changed_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "changed_by" BIGINT, -- account_id of the user who made the change (if applicable)
    "diff" JSONB NOT NULL, -- JSON diff of the record's fields (for insert only, other diff = snapshot)
    "snapshot" JSONB NOT NULL, -- Full record values after the change
    CONSTRAINT "audit_log_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "audit_log_table_name_record_id_version_key" UNIQUE ("table_name", "record_id", "version")
  );

-- Hierarchical product category tree. parent_id = NULL means root category.
-- Declared before "listing" because that table FKs it.
CREATE TABLE
  IF NOT EXISTS "category" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "parent_id" BIGINT, -- NULL = root category; else FK to parent category
    "name" VARCHAR(100) NOT NULL,
    "description" TEXT NOT NULL,
    "embedding_stale_at" TIMESTAMPTZ, -- NULL = fresh

    CONSTRAINT "category_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "category_name_key" UNIQUE ("name"),
    -- Deleting a parent promotes its children to roots.
    CONSTRAINT "category_parent_id_fkey" FOREIGN KEY ("parent_id") REFERENCES "category" ("id") ON DELETE SET NULL
  );

CREATE INDEX IF NOT EXISTS "category_parent_id_idx" ON "category" ("parent_id");

-- A listing: one seller's offer, with its own condition, price mode and slug. Not a shared
-- product master — two sellers listing the same phone are two rows, which is why the
-- seller id and the condition sit here.
-- cached_rating is denormalized from trust.review and maintained by the catalog
-- service: trust is another schema, so it cannot be joined. Price is not cached —
-- see variant_price_idx.
CREATE TABLE
  IF NOT EXISTS "listing" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "slug" VARCHAR(100) NOT NULL, -- URL-friendly slug derived from name, must be globally unique
    "account_id" BIGINT NOT NULL, -- Seller account that owns this listing
    "category_id" BIGINT NOT NULL,
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
    "shipping_paid_by" "shipping_paid_by" NOT NULL, -- who carries the delivery fee
    "currency" VARCHAR(3) NOT NULL, -- ISO 4217 currency code for every variant price under this listing

    -- Edits
    -- The edit a seller submitted against a live listing, held until a moderator applies
    -- it. NULL means none — one representation of absent, and the moderation queue asks
    -- `IS NOT NULL` rather than comparing against an empty object. Its shape is the
    -- editable subset of this row (catalogapi.PendingEdit).
    "pending_edit" JSONB,
    -- Denormalized
    "cached_rating" DOUBLE PRECISION NOT NULL DEFAULT 0, -- average trust.review rating (1..5), 0 = no reviews yet
    -- Units sold across every variant of this listing, maintained alongside "stock"."sold" —
    -- completed sales only, so an abandoned checkout never moves it. Denormalized for the
    -- same reason as the rating, and for one more: it is a SUM over the variants, and
    -- unlike the cheapest price — a MIN, which an ordered scan of variant_price_idx
    -- yields correctly with an early LIMIT — a top-N by SUM cannot be read off a per-variant
    -- index at all. A listing with five steady variants outsells one with a single hot
    -- variant, and a scan of stock_sold_idx never sees that.
    "cached_sold" BIGINT NOT NULL DEFAULT 0,

    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, -- sorts the "newest listings" feed
    -- Soft delete, because order.item holds listing_id / variant_id without an FK and order
    -- history must stay resolvable. Distinct from status='hidden', which is a live
    -- listing the seller took down temporarily.
    "deleted_at" TIMESTAMPTZ,
    "embedding_stale_at" TIMESTAMPTZ, -- NULL = fresh

    CONSTRAINT "listing_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "listing_slug_key" UNIQUE ("slug"),
    CONSTRAINT "listing_currency_format" CHECK ("currency" ~ '^[A-Z]{3}$'),
    CONSTRAINT "listing_cached_sold_non_negative" CHECK ("cached_sold" >= 0),
    -- RESTRICT: a category must be emptied before it can be deleted ("category_id"
    -- is NOT NULL, so SET NULL is not an option).
    CONSTRAINT "listing_category_id_fkey" FOREIGN KEY ("category_id") REFERENCES "category" ("id") ON DELETE RESTRICT
  );

CREATE INDEX IF NOT EXISTS "listing_account_id_idx" ON "listing" ("account_id");

CREATE INDEX IF NOT EXISTS "listing_category_id_idx" ON "listing" ("category_id");

CREATE INDEX IF NOT EXISTS "listing_name_unaccent_trgm_idx" ON "listing" USING gin ("f_unaccent" ("name") gin_trgm_ops);

-- Browse feeds, one index per sort key. Partial on live active rows, which is all a
-- buyer sees; that also covers filtering by "status", too low-cardinality to index on
-- its own. Sorting by price goes through variant_price_idx instead.
CREATE INDEX IF NOT EXISTS "listing_active_created_at_idx"
    ON "listing" ("created_at" DESC)
    WHERE "status" = 'active' AND "deleted_at" IS NULL;
CREATE INDEX IF NOT EXISTS "listing_active_cached_rating_idx"
    ON "listing" ("cached_rating" DESC)
    WHERE "status" = 'active' AND "deleted_at" IS NULL;
CREATE INDEX IF NOT EXISTS "listing_active_cached_sold_idx"
    ON "listing" ("cached_sold" DESC)
    WHERE "status" = 'active' AND "deleted_at" IS NULL;
-- The moderation queue.
CREATE INDEX IF NOT EXISTS "listing_pending_created_at_idx"
    ON "listing" ("created_at")
    WHERE "status" = 'pending' AND "deleted_at" IS NULL;

-- A purchasable variant of a listing (e.g. size=l, color=red). The row that holds a price
-- and, through "stock", the units on hand.
CREATE TABLE
  IF NOT EXISTS "variant" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "listing_id" BIGINT NOT NULL,
    "price" BIGINT NOT NULL, -- Price in smallest currency unit (e.g. VND, cents)
    "attributes" JSONB NOT NULL, -- Variant attribute key/value pairs (e.g. {"size": "l", "color": "red"})
    "package_details" JSONB NOT NULL, -- Physical packaging info for shipping (weight, dimensions, etc.)
    -- Variant-specific images; empty means fall back to the listing gallery.
    "attachments" BIGINT[] NOT NULL DEFAULT '{}',
    -- The variant shown in search results and on the product card. The flag lives here
    -- rather than as "listing"."featured_variant_id" so that "the featured variant belongs
    -- to this listing" is not a rule anybody has to enforce: the row already carries
    -- "listing_id", so the wrong listing's variant cannot be named. It also un-circles the two
    -- tables, which is why there is no trailing ALTER at the end of this file.
    "is_featured" BOOLEAN NOT NULL DEFAULT false,

    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "deleted_at" TIMESTAMPTZ, -- same soft-delete rule as listing
    CONSTRAINT "variant_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "variant_price_positive_check" CHECK ("price" >= 0),
    CONSTRAINT "variant_listing_id_fkey" FOREIGN KEY ("listing_id") REFERENCES "listing" ("id") ON DELETE CASCADE
  );

CREATE INDEX IF NOT EXISTS "variant_listing_id_idx" ON "variant" ("listing_id");
-- The ordered side of the price-sorted feed: scanned in price order and nested-looped
-- into listing, so rows come out already sorted and LIMIT stops early.
CREATE INDEX IF NOT EXISTS "variant_price_idx"
    ON "variant" ("price")
    WHERE "deleted_at" IS NULL;
-- At most one featured variant per listing — the same partial-unique pattern as
-- account.contact's default address, and it also serves "give me the card variant".
CREATE UNIQUE INDEX IF NOT EXISTS "variant_one_featured_per_listing"
    ON "variant" ("listing_id")
    WHERE "is_featured" AND "deleted_at" IS NULL;
-- Two live variants of one listing cannot describe the same combination. jsonb equality
-- ignores key order, so {"size":"l","color":"red"} is caught whichever way it was written.
CREATE UNIQUE INDEX IF NOT EXISTS "variant_listing_id_attributes_key"
    ON "variant" ("listing_id", "attributes")
    WHERE "deleted_at" IS NULL;


-- Flat tag dictionary. id is the tag slug (e.g. 'eco-friendly', 'handmade').
CREATE TABLE
  IF NOT EXISTS "tag" (
    "id" VARCHAR(100) NOT NULL,
    "description" VARCHAR(255),
    "embedding_stale_at" TIMESTAMPTZ, -- NULL = fresh
    CONSTRAINT "tag_pkey" PRIMARY KEY ("id")
  );

-- The tag picker's prefix search. "tag_pkey" cannot serve LIKE 'x%': a btree under a
-- non-C collation does not order by byte, so the planner will not turn a prefix into a
-- range on it. text_pattern_ops does, and one operator-class index is the whole cost —
-- a trigram index would buy substring matching the picker does not ask for.
CREATE INDEX IF NOT EXISTS "tag_id_prefix_idx" ON "tag" ("id" text_pattern_ops);

-- Many-to-many join between listings and tags.
CREATE TABLE
  IF NOT EXISTS "listing_tag" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "listing_id" BIGINT NOT NULL,
    "tag" VARCHAR(100) NOT NULL,
    CONSTRAINT "listing_tag_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "listing_tag_listing_id_tag_key" UNIQUE ("listing_id", "tag"),
    CONSTRAINT "listing_tag_listing_id_fkey" FOREIGN KEY ("listing_id") REFERENCES "listing" ("id") ON DELETE CASCADE,
    CONSTRAINT "listing_tag_tag_fkey" FOREIGN KEY ("tag") REFERENCES "tag" ("id") ON DELETE CASCADE ON UPDATE CASCADE
  );

-- Vector search (pgvector), both vectors from BGE-M3. Rows are created by the sync cron
-- and stay NULL until the first embedding pass.
-- "sparse" has one non-zero per distinct input token and HNSW caps that at 1000, so the
-- embedding job must keep max_length at 512-1024. The cap is enforced by the index at
-- INSERT ("sparsevec cannot have more than 1000 non-zero elements"), not at build time,
-- so an over-long input fails the write rather than degrading search.
-- ip_ops for sparse (lexical matching is a dot product), cosine for dense.
CREATE TABLE
  IF NOT EXISTS "listing_embedding" (
    "listing_id" BIGINT NOT NULL,
    "dense" vector (1024), -- dense vector; NULL until embedded
    "sparse" sparsevec (250048), -- sparse (lexical) vector; NULL until embedded. Max 1000 non-zeros
    "updated_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, -- last embedding pass, not an audit field
    CONSTRAINT "listing_embedding_pkey" PRIMARY KEY ("listing_id"),
    CONSTRAINT "listing_embedding_listing_id_fkey" FOREIGN KEY ("listing_id") REFERENCES "listing" ("id") ON DELETE CASCADE
  );

CREATE INDEX IF NOT EXISTS "listing_embedding_dense_idx" ON "listing_embedding" USING hnsw ("dense" vector_cosine_ops);

CREATE INDEX IF NOT EXISTS "listing_embedding_sparse_idx" ON "listing_embedding" USING hnsw ("sparse" sparsevec_ip_ops);

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
-- listing_embedding, not here, and a few slots per account cost nothing to scan. No vector
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

-- Wishlist / saved listings. account_id is a cross-module ref to account.account (no FK,
-- same as account_interest above); listing_id is local, which is the point of the table
-- living here — "is this saved", "how many saved it" and "my saved listings" are all
-- joins against listing rather than calls into another module.
-- The pair is the whole row, so it is the key.
CREATE TABLE IF NOT EXISTS "favorite" (
    "account_id" BIGINT NOT NULL,
    "listing_id" BIGINT NOT NULL,
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "favorite_pkey" PRIMARY KEY ("account_id", "listing_id"),
    CONSTRAINT "favorite_listing_id_fkey" FOREIGN KEY ("listing_id")
        REFERENCES "listing" ("id") ON DELETE CASCADE
);
-- "how many saved this listing" / "who saved it".
CREATE INDEX IF NOT EXISTS "favorite_listing_id_idx" ON "favorite" ("listing_id");
-- The wishlist page, newest first: the PK covers the lookup but not the ordering.
CREATE INDEX IF NOT EXISTS "favorite_account_id_created_at_idx"
    ON "favorite" ("account_id", "created_at" DESC);

-- Stock level for one variant, deliberately *not* columns on "variant" even though it is
-- 1-1 with a variant: the two halves are written at different rhythms. "reserved" moves on every
-- checkout (hot, contended, driven by the order module) while the variant row is written
-- when a seller edits the listing. Merged, every reservation would touch the row that
-- feeds the price index and collide with every seller edit.
CREATE TABLE IF NOT EXISTS "stock" (
    -- Keyed by the variant: the row has no identity of its own, the same shape as
    -- "listing_embedding". A surrogate id plus a UNIQUE would let a second row for one variant
    -- be attempted and rejected; this way it cannot be written at all.
    "variant_id" BIGINT NOT NULL,
    "quantity" BIGINT NOT NULL, -- total units the seller has
    -- Two counters, not one: "reserved" is held by a checkout that has not completed and
    -- comes back on cancellation, "sold" never goes down. One combined column made "how
    -- many sold" a number that inflates with abandoned carts and drops when one expires.
    "reserved" BIGINT NOT NULL DEFAULT 0,
    "sold" BIGINT NOT NULL DEFAULT 0,
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "stock_pkey" PRIMARY KEY ("variant_id"),
    -- Without the last one an oversold row is a legal row.
    CONSTRAINT "stock_quantity_non_negative" CHECK ("quantity" >= 0),
    CONSTRAINT "stock_reserved_non_negative" CHECK ("reserved" >= 0),
    CONSTRAINT "stock_sold_non_negative" CHECK ("sold" >= 0),
    CONSTRAINT "stock_committed_within_quantity" CHECK ("reserved" + "sold" <= "quantity"),

    CONSTRAINT "stock_variant_id_fkey" FOREIGN KEY ("variant_id")
        REFERENCES "variant" ("id") ON DELETE CASCADE
);
-- Per-variant "best seller", for a seller looking at which variant moves. Listing-level
-- best-selling does NOT come from here — see listing.cached_sold for why a SUM over
-- variants cannot be read off this index.
CREATE INDEX IF NOT EXISTS "stock_sold_idx" ON "stock" ("sold" DESC);
