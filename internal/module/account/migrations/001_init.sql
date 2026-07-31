-- Module: account — canonical schema
-- Description: User accounts, federated identities, profiles, contacts, push
--              devices, notifications and their per-channel preferences, payout
--              identity verification, and the seller follow graph. Any account can act
--              as both buyer and seller.
--              Wishlists live in catalog: every question asked of a saved listing is a
--              catalog question, so keeping the row here would have made all three of
--              them a cross-module call.

-- Geographic point type for contact locations (distance-based shipping promos,
-- nearest-seller lookups). geography validates coordinate ranges itself.
CREATE EXTENSION IF NOT EXISTS postgis
WITH
  SCHEMA public;

-- "notification" is a hypertable; account migrates first, so it cannot rely on chat or
-- observability having added the extension.
CREATE EXTENSION IF NOT EXISTS timescaledb;

-- Trigram matching for the admin account search. Declared here as well as in catalog
-- for the same reason as the two above: account migrates first, and once the modules
-- sit on separate databases each one has to bring what it needs.
CREATE EXTENSION IF NOT EXISTS pg_trgm
WITH
  SCHEMA public;

-- Enums

-- Account lifecycle state
CREATE TYPE "account_status" AS ENUM ('active', 'suspended');
-- Role-based access control. 'admin' is granted manually by ops; new signups
-- default to 'user'.
CREATE TYPE "account_role" AS ENUM ('user', 'moderator', 'admin');
-- Self-reported gender for the profile
CREATE TYPE "profile_gender" AS ENUM ('male', 'female', 'other');
-- Address classification for contacts
CREATE TYPE "contact_address_type" AS ENUM ('home', 'work');
-- Push device platform
CREATE TYPE "device_platform" AS ENUM ('ios', 'android', 'web');

-- Government ID accepted for payout KYC
CREATE TYPE "identity_document_type" AS ENUM ('national-id', 'passport', 'driver-license');
-- Outcome of one identity check
CREATE TYPE "identity_status" AS ENUM ('pending', 'verified', 'rejected');

-- Delivery channel
CREATE TYPE "notification_type" AS ENUM ('in-app', 'push', 'email', 'sms');
-- What a notification is about. An enum because "notification_preference" keys off
-- it, and an unknown category there reads as "no preference" and gets sent anyway.
CREATE TYPE "notification_category" AS ENUM ('order', 'promotion', 'system', 'chat', 'social');

-- Tables


-- Core identity record. Each of phone/email/username is optional on its own, but
-- "account_has_identifier" requires at least one. They are stored normalized (E.164
-- phone, lowercase email and username), which is what makes plain UNIQUE enough.
-- The display columns live here rather than in a 1-1 "profile" table: a display name is
-- mandatory, created in the same statement, loaded with every command and written by the
-- same UPDATE, so splitting it bought a join and a second write and nothing else.
CREATE TABLE IF NOT EXISTS "account" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    -- Optimistic lock: every aggregate write is `WHERE version = @version` and bumps it,
    -- so a command built on a stale read is refused instead of overwriting it.
    "version" BIGINT NOT NULL DEFAULT 1,
    "status" "account_status" NOT NULL DEFAULT 'active',
    "role" "account_role" NOT NULL DEFAULT 'user',
    "phone" VARCHAR(16), -- E.164: '+' plus up to 15 digits
    "email" VARCHAR(255),
    "username" VARCHAR(100),
    "password_hash" VARCHAR(255), -- bcrypt; NULL on a provider-only account

    "email_verified" BOOLEAN NOT NULL DEFAULT false,
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    -- Set together with status = 'suspended'; NULL "suspended_until" means permanent.
    "suspended_until" TIMESTAMPTZ,
    "suspension_reason" TEXT,

    -- The public face, which doubles as the shop page.
    "name" VARCHAR(100) NOT NULL,
    "description" TEXT,
    "gender" "profile_gender",
    "date_of_birth" DATE, -- age rules are enforced in the domain
    "avatar_resource_id" BIGINT, -- not unique: accounts may share a resource, e.g. a default avatar
    "country" VARCHAR(2) NOT NULL, -- ISO 3166-1 alpha-2; picks the currency of finance.wallet
    "locale" VARCHAR(10) NOT NULL, -- BCP 47, e.g. 'vi-VN'; notification + UI language
    "timezone" VARCHAR(64) NOT NULL, -- IANA name; renders times, schedules notifications

    CONSTRAINT "account_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "account_phone_key" UNIQUE ("phone"),
    CONSTRAINT "account_email_key" UNIQUE ("email"),
    CONSTRAINT "account_username_key" UNIQUE ("username"),
    -- An account nobody can be addressed by cannot sign in. OAuth-only signup still
    -- has to fill one: generate a username when the provider returns no email.
    CONSTRAINT "account_has_identifier" CHECK (
        COALESCE("phone", "email", "username") IS NOT NULL
    ),
    CONSTRAINT "account_phone_e164" CHECK ("phone" ~ '^\+[1-9][0-9]{7,14}$'),
    CONSTRAINT "account_email_lowercase" CHECK ("email" = lower("email")),
    CONSTRAINT "account_username_lowercase" CHECK ("username" = lower("username")),
    -- Details only exist while suspended; past suspensions live in "audit_log".
    CONSTRAINT "account_suspension_requires_suspended" CHECK (
        "status" = 'suspended'
        OR ("suspended_until" IS NULL AND "suspension_reason" IS NULL)
    ),
    CONSTRAINT "account_country_format" CHECK ("country" ~ '^[A-Z]{2}$'),
    CONSTRAINT "account_locale_format" CHECK ("locale" ~ '^[a-z]{2}(-[A-Z]{2})?$'),
    CONSTRAINT "account_date_of_birth_sane" CHECK ("date_of_birth" > DATE '1900-01-01')
);
-- The display-name half of the admin account search. The identifier half needs no index
-- of its own: phone, email and username are each UNIQUE, so an exact match is already a
-- key lookup. A name is not unique and is searched by fragment, which only a trigram
-- index can serve.
CREATE INDEX IF NOT EXISTS "account_name_trgm_idx" ON "account" USING gin ("name" gin_trgm_ops);
-- The reinstatement job: temporary suspensions that have run out. Without it a
-- suspension with a deadline never actually ends, because nothing else in the system
-- looks at "suspended_until" — a permanent one leaves it NULL and is skipped here.
CREATE INDEX IF NOT EXISTS "account_suspension_expiring_idx"
    ON "account" ("suspended_until")
    WHERE "status" = 'suspended' AND "suspended_until" IS NOT NULL;

-- Federated login identities linked to an account. An account may have a NULL
-- "account"."password" and log in through these alone.
--
-- Deliberately holds no email of its own. An email the provider asserts is matched
-- against "account"."email" and login merges into that account, so the address
-- lives there and nowhere else; only a provider-verified email may merge, or an
-- unverified one takes over whichever account it collides with. A provider email
-- that later drifts is not tracked — "account"."email" stays canonical, and the
-- value asserted at link time is recoverable from "audit_log".
CREATE TABLE IF NOT EXISTS "oauth_identity" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "account_id" BIGINT NOT NULL,
    "provider" VARCHAR(30) NOT NULL, -- kebab-case: 'google', 'facebook', 'apple', 'zalo'
    "provider_uid" VARCHAR(255) NOT NULL, -- the provider's stable subject id, never the email
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "oauth_identity_pkey" PRIMARY KEY ("id"),
    -- One provider account maps to one local account, and one identity per provider.
    CONSTRAINT "oauth_identity_provider_provider_uid_key" UNIQUE ("provider", "provider_uid"),
    CONSTRAINT "oauth_identity_account_id_provider_key" UNIQUE ("account_id", "provider"),

    CONSTRAINT "oauth_identity_account_id_fkey" FOREIGN KEY ("account_id")
        REFERENCES "account" ("id") ON DELETE CASCADE
);

-- Registered push targets, one row per install. Delivery of 'push' notifications
-- fans out over the rows of the recipient account.
CREATE TABLE IF NOT EXISTS "device" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "account_id" BIGINT NOT NULL,
    "platform" "device_platform" NOT NULL,
    "push_token" TEXT NOT NULL, -- FCM / APNs registration token
    "last_seen_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, -- prune stale tokens by this
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "device_pkey" PRIMARY KEY ("id"),
    -- Globally unique, not per-account: the token identifies an install, and the
    -- provider reissues the same one when another user signs in on that phone. Upsert
    -- on it so the row moves accounts, or the old owner keeps getting those pushes.
    CONSTRAINT "device_push_token_key" UNIQUE ("push_token"),

    CONSTRAINT "device_account_id_fkey" FOREIGN KEY ("account_id")
        REFERENCES "account" ("id") ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS "device_account_id_idx" ON "device" ("account_id");

-- Saved addresses and contact details used for shipping and billing.
-- Carrier APIs (GHN, GHTK, Viettel Post) are called with the administrative codes, so
-- those are the routing source of truth; "location" is advisory, for last-mile hints
-- and distance-based features.
CREATE TABLE IF NOT EXISTS "contact" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "account_id" BIGINT NOT NULL,
    "full_name" VARCHAR(100) NOT NULL,
    "phone" VARCHAR(16) NOT NULL, -- E.164, same normalization as account.phone
    "phone_verified" BOOLEAN NOT NULL DEFAULT false,
    "address_type" "contact_address_type" NOT NULL,
    -- A default per role: where orders arrive as a buyer, where carriers collect as a
    -- seller. Independent flags — the same address is usually both.
    "is_default_delivery" BOOLEAN NOT NULL DEFAULT false,
    "is_default_pickup" BOOLEAN NOT NULL DEFAULT false,
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    -- Administrative levels. "district_code" is nullable: Vietnam has no district tier
    -- (province → ward), other countries do. Names are display snapshots, so a later
    -- territorial rename does not rewrite a saved address.
    "country" VARCHAR(2) NOT NULL, -- ISO 3166-1 alpha-2
    "province_code" VARCHAR(20) NOT NULL,
    "province_name" VARCHAR(100) NOT NULL,
    "district_code" VARCHAR(20),
    "district_name" VARCHAR(100),
    "ward_code" VARCHAR(20) NOT NULL,
    "ward_name" VARCHAR(100) NOT NULL,
    "postal_code" VARCHAR(20),
    -- Per-carrier territory ids, e.g. {"ghn": {"province_id": 201, "district_id": 1442}}.
    -- Carriers number territories their own way, and some still require a district.
    "provider_codes" JSONB NOT NULL DEFAULT '{}',

    "address" VARCHAR(255) NOT NULL, -- street / house number line, below ward level
    "address_detail" VARCHAR(255), -- unit/floor/notes; free text, never geocoded
    "location" geography(Point, 4326), -- NULL when geocoding failed; must be near the text address

    CONSTRAINT "contact_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "contact_country_format" CHECK ("country" ~ '^[A-Z]{2}$'),
    CONSTRAINT "contact_phone_e164" CHECK ("phone" ~ '^\+[1-9][0-9]{7,14}$'),
    CONSTRAINT "contact_district_code_name_together" CHECK (
        ("district_code" IS NULL) = ("district_name" IS NULL)
    ),

    CONSTRAINT "contact_account_id_fkey" FOREIGN KEY ("account_id")
        REFERENCES "account" ("id") ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS "contact_account_id_idx" ON "contact" ("account_id");
-- At most one default per role per account (same pattern as finance.bank_account).
CREATE UNIQUE INDEX IF NOT EXISTS "contact_one_default_delivery_per_account"
    ON "contact" ("account_id")
    WHERE "is_default_delivery";
CREATE UNIQUE INDEX IF NOT EXISTS "contact_one_default_pickup_per_account"
    ON "contact" ("account_id")
    WHERE "is_default_pickup";
-- Distance queries (nearest seller, shipping-radius promos).
CREATE INDEX IF NOT EXISTS "contact_location_idx" ON "contact" USING GIST ("location");

-- In-app notifications: what the user is told, once, whatever channels it went out on.
-- The fan-out itself is a Restate workflow and keeps no state here.
CREATE TABLE IF NOT EXISTS "notification" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "account_id" BIGINT NOT NULL,
    "category" "notification_category" NOT NULL, -- what it is about; also the preference key
    "title" VARCHAR(200) NOT NULL,
    "payload" JSONB NOT NULL, -- structured payload (deep-links, images, etc.)

    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "read_at" TIMESTAMPTZ, -- NULL = unread; the timestamp is what "mark all read" needs
    "scheduled_at" TIMESTAMPTZ, -- future dispatch time; NULL means send immediately

    -- "created_at" joins the key because a unique index on a hypertable must contain
    -- its partitioning column.
    CONSTRAINT "notification_pkey" PRIMARY KEY ("id", "created_at"),

    CONSTRAINT "notification_account_id_fkey" FOREIGN KEY ("account_id")
        REFERENCES "account" ("id") ON DELETE CASCADE
);
-- Chunked so the fastest-growing table in this module stays bounded for vacuum and
-- index maintenance, and old chunks can be dropped whole.
SELECT create_hypertable('notification', 'created_at', chunk_time_interval => INTERVAL '7 days', if_not_exists => TRUE);
-- Unlike chat.message, a notification is not evidence in a dispute — "your order
-- shipped" from last year has no reader. Six months, then the chunk goes.
SELECT add_retention_policy('notification', INTERVAL '180 days', if_not_exists => TRUE);

-- The account's feed, newest first.
CREATE INDEX IF NOT EXISTS "notification_account_id_created_at_idx"
    ON "notification" ("account_id", "created_at" DESC);
-- The unread badge count, and the unread-only feed: "created_at" is in the key so that
-- filtering to unread still comes out in feed order and a cursor can seek into it,
-- rather than reading every unread row to sort them.
CREATE INDEX IF NOT EXISTS "notification_account_id_unread_idx"
    ON "notification" ("account_id", "created_at" DESC)
    WHERE "read_at" IS NULL;

-- There is no per-channel delivery table. Fanning one notification out to push, email
-- and SMS is a durable workflow — attempt, retry with backoff, give up — and Restate
-- already keeps that journal. A second copy in Postgres would be a queue nobody drains
-- and a status nobody is the authority on.

-- Per-account, per-channel opt-outs. Sparse: a row exists only where the account
-- deviates from the default, so "no row" means default. Defaults live in the domain —
-- they differ per category and change without a migration. Changes go to "audit_log".
-- Nothing here stops an account opting out of category 'system': which notices are
-- mandatory is a product rule, so the domain decides and this table just records it.
CREATE TABLE IF NOT EXISTS "notification_preference" (
    "account_id" BIGINT NOT NULL,
    "category" "notification_category" NOT NULL,
    "channel" "notification_type" NOT NULL,
    "is_enabled" BOOLEAN NOT NULL,
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    -- The triple is the whole row, so it is the key; its first column also serves
    -- "load every preference of this account".
    CONSTRAINT "notification_preference_pkey" PRIMARY KEY ("account_id", "category", "channel"),

    CONSTRAINT "notification_preference_account_id_fkey" FOREIGN KEY ("account_id")
        REFERENCES "account" ("id") ON DELETE CASCADE
);

-- "favorite" lives in catalog, not here. All three questions asked of a wishlist — is
-- this listing saved, how many saved it, show me my saved listings — are answered
-- against catalog.listing, so the row belongs on that side of the line: as a catalog table
-- it is a plain join, and here it would have made every one of them a cross-module call.
-- "follow" stays: both of its sides are accounts.

-- Seller follow graph. Both sides are accounts, since any account can sell.
CREATE TABLE IF NOT EXISTS "follow" (
    "follower_id" BIGINT NOT NULL, -- the account doing the following
    "followee_id" BIGINT NOT NULL, -- the account (seller) being followed
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "follow_pkey" PRIMARY KEY ("follower_id", "followee_id"),
    CONSTRAINT "follow_no_self_follow" CHECK ("follower_id" <> "followee_id"),

    CONSTRAINT "follow_follower_id_fkey" FOREIGN KEY ("follower_id")
        REFERENCES "account" ("id") ON DELETE CASCADE,
    CONSTRAINT "follow_followee_id_fkey" FOREIGN KEY ("followee_id")
        REFERENCES "account" ("id") ON DELETE CASCADE
);
-- "who follows this seller", newest first. Also the counter for the shop page.
CREATE INDEX IF NOT EXISTS "follow_followee_id_idx" ON "follow" ("followee_id", "created_at" DESC);
-- "sellers I follow", newest first: the PK covers the lookup but not the ordering.
CREATE INDEX IF NOT EXISTS "follow_follower_id_created_at_idx" ON "follow" ("follower_id", "created_at" DESC);

-- Government-ID verification, required for payout in many markets. Lives in "account"
-- rather than "finance": it establishes who the person is, and linking it to
-- "account"."status" (a suspension follows a rejected check) stays same-schema.
-- Deliberately holds no document number and no scan: a KYC provider does the check
-- and only its verdict is kept, so leaking this table cannot impersonate anyone.
-- The scans themselves, if any, belong in "common"."resource" behind the provider.
CREATE TABLE IF NOT EXISTS "identity_document" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "account_id" BIGINT NOT NULL,
    "doc_type" "identity_document_type" NOT NULL,
    "provider" VARCHAR(30) NOT NULL, -- kebab-case KYC vendor, e.g. 'vnpt-ekyc'
    "provider_ref" TEXT NOT NULL, -- the vendor's case id, for re-reading the verdict
    "status" "identity_status" NOT NULL DEFAULT 'pending',
    "rejection_reason" TEXT,
    "verified_at" TIMESTAMPTZ,
    -- Passports and IDs expire; a payout check has to look at more than the status.
    "expires_at" TIMESTAMPTZ,
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "identity_document_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "identity_document_provider_ref_key" UNIQUE ("provider", "provider_ref"),
    CONSTRAINT "identity_document_provider_format" CHECK ("provider" ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    -- Each of the two outcome fields belongs to exactly one status.
    CONSTRAINT "identity_document_verified_at_matches_status" CHECK (
        ("status" = 'verified') = ("verified_at" IS NOT NULL)
    ),
    CONSTRAINT "identity_document_rejection_requires_rejected" CHECK (
        "status" = 'rejected' OR "rejection_reason" IS NULL
    ),

    CONSTRAINT "identity_document_account_id_fkey" FOREIGN KEY ("account_id")
        REFERENCES "account" ("id") ON DELETE CASCADE
);
-- One live verified identity per account, and it makes "is this account verified"
-- an index lookup for the payout gate instead of a scan.
CREATE UNIQUE INDEX IF NOT EXISTS "identity_document_one_verified_per_account"
    ON "identity_document" ("account_id")
    WHERE "status" = 'verified';
-- The admin review queue.
CREATE INDEX IF NOT EXISTS "identity_document_pending_idx"
    ON "identity_document" ("created_at")
    WHERE "status" = 'pending';
-- The re-verification job: documents that have run out.
CREATE INDEX IF NOT EXISTS "identity_document_expiring_idx"
    ON "identity_document" ("expires_at")
    WHERE "status" = 'verified' AND "expires_at" IS NOT NULL;
