-- Shared DDL: applied into every module's schema by cmd/migrate, so the table exists
-- once as text and once per schema. Seven modules used to carry a copy of this and four
-- of them had already drifted in the comments.
--
-- Per-schema rather than one global table, because schema isolation is what lets a module
-- move to its own database: a shared audit table would be the one thing that could not go
-- with it.
CREATE TABLE
  IF NOT EXISTS "audit_log" (
    "id" BIGINT GENERATED ALWAYS AS IDENTITY,
    "version" BIGINT NOT NULL DEFAULT 1, -- incremented on each change to the same record
    "table_name" VARCHAR(100) NOT NULL,
    "record_id" BIGINT NOT NULL,
    "change_type" VARCHAR(10) NOT NULL, -- 'insert', 'update', 'delete'
    -- The business code the module recorded: 'listing.publish', 'account.suspend'.
    "code" VARCHAR(100) NOT NULL,
    "changed_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "changed_by" BIGINT, -- the account responsible; NULL for a job or a vendor callback
    "diff" JSONB NOT NULL, -- what the recorded fact carried
    "snapshot" JSONB NOT NULL, -- the record as it is after the change
    CONSTRAINT "audit_log_pkey" PRIMARY KEY ("id"),
    CONSTRAINT "audit_log_table_name_record_id_version_key" UNIQUE ("table_name", "record_id", "version")
  );
