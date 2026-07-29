-- Module: account — canonical schema
-- Description: User accounts, federated identities, profiles, contacts, push
--              devices, notifications (content + per-channel delivery) and their
--              preferences, payout identity verification, favorites, and the
--              seller follow graph. Any account can act as both buyer and seller.

-- Geographic point type for contact locations (distance-based shipping promos,
-- nearest-seller lookups). geography validates coordinate ranges itself.
CREATE EXTENSION IF NOT EXISTS postgis
WITH
  SCHEMA public;

-- "notification" and "notification_delivery" are hypertables; account migrates first,
-- so it cannot rely on chat or observability having added the extension.
CREATE EXTENSION IF NOT EXISTS timescaledb;

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

-- Per-channel delivery outcome of one notification
CREATE TYPE "notification_delivery_status" AS ENUM ('pending', 'sent', 'failed');

-- Delivery channel
CREATE TYPE "notification_type" AS ENUM ('in-app', 'push', 'email', 'sms');
-- What a notification is about. An enum because "notification_preference" keys off
-- it, and an unknown category there reads as "no preference" and gets sent anyway.
CREATE TYPE "notification_category" AS ENUM ('order', 'promotion', 'system', 'chat', 'social');

-- Tables

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

-- Core identity record. Each of phone/email/username is optional on its own, but
-- "account_has_identifier" requires at least one. They are stored normalized (E.164
-- phone, lowercase email and username), which is what makes plain UNIQUE enough.
CREATE TABLE IF NOT EXISTS "account" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "status" "account_status" NOT NULL DEFAULT 'active',
    "role" "account_role" NOT NULL DEFAULT 'user',
    "phone" VARCHAR(16), -- E.164: '+' plus up to 15 digits
    "email" VARCHAR(255),
    "username" VARCHAR(100),
    "password" VARCHAR(255),

    "email_verified" BOOLEAN NOT NULL DEFAULT false,
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    -- Set together with status = 'suspended'; NULL "suspended_until" means permanent.
    "suspended_until" TIMESTAMPTZ,
    "suspension_reason" TEXT,

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
    )
);

-- Federated login identities linked to an account. An account may have a NULL
-- "account"."password" and log in through these alone.
CREATE TABLE IF NOT EXISTS "oauth_identity" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "account_id" BIGINT NOT NULL,
    "provider" VARCHAR(30) NOT NULL, -- kebab-case: 'google', 'facebook', 'apple', 'zalo'
    "provider_uid" VARCHAR(255) NOT NULL, -- the provider's stable subject id, never the email
    "email" VARCHAR(255), -- as reported by the provider; may differ from account.email and is not authoritative
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

-- Extended public profile details; 1-1 with account via shared PK.
CREATE TABLE IF NOT EXISTS "profile" (
    "id" BIGINT NOT NULL,
    "gender" "profile_gender",
    "name" VARCHAR(100) NOT NULL,
    "description" TEXT,
    "date_of_birth" DATE, -- age rules are enforced in the domain
    "avatar_resource_id" BIGINT, -- not unique: accounts may share a resource, e.g. a default avatar
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    "country" VARCHAR(2) NOT NULL, -- ISO 3166-1 alpha-2; picks the currency of finance.wallet
    "locale" VARCHAR(10) NOT NULL, -- BCP 47, e.g. 'vi-VN'; notification + UI language
    "timezone" VARCHAR(64) NOT NULL, -- IANA name, e.g. 'Asia/Ho_Chi_Minh'; renders times, schedules notifications

    CONSTRAINT "profile_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "profile_country_format" CHECK ("country" ~ '^[A-Z]{2}$'),
    CONSTRAINT "profile_locale_format" CHECK ("locale" ~ '^[a-z]{2}(-[A-Z]{2})?$'),
    CONSTRAINT "profile_date_of_birth_sane" CHECK ("date_of_birth" > DATE '1900-01-01'),

    -- profile shares the same PK as account (1-1 relationship)
    CONSTRAINT "profile_id_fkey" FOREIGN KEY ("id")
        REFERENCES "account" ("id") ON DELETE CASCADE
);

-- In-app notifications delivered via various channels (push, email, SMS, etc.).
-- What the user is told, once. The channels it goes out on are rows in
-- "notification_delivery": four channels used to mean four copies of title+payload.
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
-- The unread badge count.
CREATE INDEX IF NOT EXISTS "notification_account_id_unread_idx"
    ON "notification" ("account_id")
    WHERE "read_at" IS NULL;

-- One row per channel a notification is pushed out on, with its own retry state.
-- No FK to "notification": retention drops whole chunks there, which an FK would
-- either block or leave dangling. The pair of retention windows keeps them in step.
CREATE TABLE IF NOT EXISTS "notification_delivery" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "notification_id" BIGINT NOT NULL, -- same schema, but no FK (see above)
    "account_id" BIGINT NOT NULL, -- denormalized so the dispatcher need not join
    "channel" "notification_type" NOT NULL,
    "status" "notification_delivery_status" NOT NULL DEFAULT 'pending',
    "attempts" INT NOT NULL DEFAULT 0,
    "last_error" TEXT, -- provider message from the most recent failed attempt
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "sent_at" TIMESTAMPTZ, -- set when the provider accepted it

    CONSTRAINT "notification_delivery_pkey" PRIMARY KEY ("id", "created_at"),
    CONSTRAINT "notification_delivery_attempts_non_negative" CHECK ("attempts" >= 0),
    -- 'sent' and a send timestamp are the same fact; neither exists without the other.
    CONSTRAINT "notification_delivery_sent_at_matches_status" CHECK (
        ("status" = 'sent') = ("sent_at" IS NOT NULL)
    ),

    CONSTRAINT "notification_delivery_account_id_fkey" FOREIGN KEY ("account_id")
        REFERENCES "account" ("id") ON DELETE CASCADE
);
SELECT create_hypertable('notification_delivery', 'created_at', chunk_time_interval => INTERVAL '7 days', if_not_exists => TRUE);
SELECT add_retention_policy('notification_delivery', INTERVAL '180 days', if_not_exists => TRUE);

-- Every channel a given notification went out on.
CREATE INDEX IF NOT EXISTS "notification_delivery_notification_id_idx"
    ON "notification_delivery" ("notification_id");
-- The dispatcher's queue: what still has to go out.
CREATE INDEX IF NOT EXISTS "notification_delivery_pending_idx"
    ON "notification_delivery" ("created_at")
    WHERE "status" = 'pending';

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

-- Wishlist / saved products. spu_id references catalog.product_spu (no FK enforced).
-- The pair is the whole row, so it is the key.
CREATE TABLE IF NOT EXISTS "favorite" (
    "account_id" BIGINT NOT NULL,
    "spu_id" BIGINT NOT NULL,
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "favorite_pkey" PRIMARY KEY ("account_id", "spu_id"),

    CONSTRAINT "favorite_account_id_fkey" FOREIGN KEY ("account_id")
        REFERENCES "account" ("id") ON DELETE CASCADE
);
-- "how many saved this product" / "who saved it".
CREATE INDEX IF NOT EXISTS "favorite_spu_id_idx" ON "favorite" ("spu_id");
-- The wishlist page, newest first: the PK covers the lookup but not the ordering.
CREATE INDEX IF NOT EXISTS "favorite_account_id_created_at_idx"
    ON "favorite" ("account_id", "created_at" DESC);

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
