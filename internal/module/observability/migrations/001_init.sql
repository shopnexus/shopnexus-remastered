-- Operational telemetry stored as TimescaleDB hypertables (schema: observability,
-- set via search_path). Grafana reads these directly (Postgres datasource).
-- Product/web analytics is handled outside the backend (Rybbit + ClickHouse).
--
-- Every table carries "instance": without it several pods collapse into one series.
-- Rows can be duplicated — the JetStream consumer is at-least-once, so a redelivered
-- batch is counted twice; accepted rather than a dedup key on the hot write path.
-- Everything is compressed after a week and dropped on its retention window, because
-- telemetry kept forever takes down the database it is monitoring.
CREATE EXTENSION IF NOT EXISTS timescaledb;

-- Percentiles: avg hides the tail, and a plain percentile_cont cannot be kept in a
-- continuous aggregate because it needs every row. The toolkit's sketch types are
-- partial-aggregatable, so p95 gets materialized like count and avg.
CREATE EXTENSION IF NOT EXISTS timescaledb_toolkit
WITH
  SCHEMA public;

-- HTTP RED: one row per request. route is the ServeMux pattern (low cardinality).
CREATE TABLE IF NOT EXISTS "http_requests" (
    "ts"          TIMESTAMPTZ      NOT NULL DEFAULT now(),
    "instance"    TEXT             NOT NULL, -- pod / host that served the request
    "method"      TEXT             NOT NULL,
    "route"       TEXT             NOT NULL,
    "status"      INT              NOT NULL,
    "duration_ms" DOUBLE PRECISION NOT NULL
);
-- 1-day chunks instead of the 7-day default: retention and compression then act at a
-- finer grain, and a time-bounded query excludes more chunks.
SELECT create_hypertable('http_requests', 'ts', chunk_time_interval => INTERVAL '1 day', if_not_exists => TRUE);
CREATE INDEX IF NOT EXISTS "http_requests_route_ts_idx" ON "http_requests" ("route", "ts" DESC);
-- The error-rate panel. 5xx is a small minority, so this stays tiny.
CREATE INDEX IF NOT EXISTS "http_requests_errors_ts_idx" ON "http_requests" ("ts" DESC) WHERE "status" >= 500;
-- Columnstore ("compress_*" is the pre-2.18 spelling of the same thing). Segment by
-- route so a per-route query reads only its own segments.
ALTER TABLE "http_requests" SET (
    timescaledb.enable_columnstore = true,
    timescaledb.segmentby = 'route',
    timescaledb.orderby = 'ts DESC'
);
CALL add_columnstore_policy('http_requests', after => INTERVAL '7 days', if_not_exists => TRUE);
SELECT add_retention_policy('http_requests', INTERVAL '30 days', if_not_exists => TRUE);

-- Outbound dependency calls (llm/payment/transport providers): one row per HTTP
-- attempt, recorded from the client transport by httpx.ObserveOutbound.
-- status 0 means no response arrived (dial error, timeout, cancellation).
-- duration_ms is time-to-response-headers, so for a streamed response it is
-- time-to-first-byte rather than the full generation time.
CREATE TABLE IF NOT EXISTS "provider_calls" (
    "ts"          TIMESTAMPTZ      NOT NULL DEFAULT now(),
    "instance"    TEXT             NOT NULL, -- pod / host that made the call
    "provider"    TEXT             NOT NULL, -- "litellm", "vnpay", …
    "method"      TEXT             NOT NULL,
    -- Templated path, no query string: the query string can carry credentials, and an
    -- id substituted into the path would make GROUP BY path explode.
    "path"        TEXT             NOT NULL,
    "status"      INT              NOT NULL,
    "duration_ms" DOUBLE PRECISION NOT NULL,
    "failed"      BOOLEAN          NOT NULL, -- transport error or 5xx; a 4xx is a valid answer
    "error"       TEXT             NOT NULL DEFAULT ''
);
SELECT create_hypertable('provider_calls', 'ts', chunk_time_interval => INTERVAL '1 day', if_not_exists => TRUE);
CREATE INDEX IF NOT EXISTS "provider_calls_provider_ts_idx" ON "provider_calls" ("provider", "ts" DESC);
ALTER TABLE "provider_calls" SET (
    timescaledb.enable_columnstore = true,
    timescaledb.segmentby = 'provider',
    timescaledb.orderby = 'ts DESC'
);
CALL add_columnstore_policy('provider_calls', after => INTERVAL '7 days', if_not_exists => TRUE);
SELECT add_retention_policy('provider_calls', INTERVAL '30 days', if_not_exists => TRUE);

-- Business events: mirror of bus events (order.placed, …) for backend dashboards.
-- Mirror ids, amounts and statuses — not personal data. Grafana reads this schema with
-- one datasource, so anything landing here is visible to everyone with a dashboard,
-- which is a wider audience than the tables the event came from.
CREATE TABLE IF NOT EXISTS "business_events" (
    "ts"       TIMESTAMPTZ NOT NULL DEFAULT now(),
    "instance" TEXT        NOT NULL,
    "topic"    TEXT        NOT NULL,
    "payload"  JSONB       NOT NULL
);
SELECT create_hypertable('business_events', 'ts', chunk_time_interval => INTERVAL '1 day', if_not_exists => TRUE);
CREATE INDEX IF NOT EXISTS "business_events_topic_ts_idx" ON "business_events" ("topic", "ts" DESC);
ALTER TABLE "business_events" SET (
    timescaledb.enable_columnstore = true,
    timescaledb.segmentby = 'topic',
    timescaledb.orderby = 'ts DESC'
);
CALL add_columnstore_policy('business_events', after => INTERVAL '7 days', if_not_exists => TRUE);
SELECT add_retention_policy('business_events', INTERVAL '180 days', if_not_exists => TRUE);

-- Go runtime: sampled periodically. No secondary index — it is always read over a time
-- range, and the hypertable's own "ts" index already covers that.
CREATE TABLE IF NOT EXISTS "runtime_metrics" (
    "ts"               TIMESTAMPTZ      NOT NULL DEFAULT now(),
    "instance"         TEXT             NOT NULL,
    "goroutines"       INT              NOT NULL,
    "heap_alloc_bytes" BIGINT           NOT NULL,
    "heap_inuse_bytes" BIGINT           NOT NULL,
    "gc_pause_ms"      DOUBLE PRECISION NOT NULL,
    "num_gc"           BIGINT           NOT NULL
);
SELECT create_hypertable('runtime_metrics', 'ts', chunk_time_interval => INTERVAL '7 days', if_not_exists => TRUE);
ALTER TABLE "runtime_metrics" SET (
    timescaledb.enable_columnstore = true,
    timescaledb.segmentby = 'instance',
    timescaledb.orderby = 'ts DESC'
);
CALL add_columnstore_policy('runtime_metrics', after => INTERVAL '7 days', if_not_exists => TRUE);
SELECT add_retention_policy('runtime_metrics', INTERVAL '90 days', if_not_exists => TRUE);

-- Pre-aggregated RED so panels never scan raw rows. avg and max only: a real p95 needs
-- percentile_agg from timescaledb_toolkit, and adding it later means rebuilding this view.
-- materialized_only = false unions the live tail in, otherwise the newest minute or two
-- is missing from every panel.
CREATE MATERIALIZED VIEW IF NOT EXISTS "http_requests_1m"
WITH (timescaledb.continuous, timescaledb.materialized_only = false) AS
SELECT time_bucket(INTERVAL '1 minute', "ts") AS "bucket",
       "instance",
       "route",
       "status",
       count(*)             AS "calls",
       avg("duration_ms")   AS "avg_ms",
       max("duration_ms")   AS "max_ms",
       -- A UddSketch, not a number: read it with approx_percentile(0.95, "latency"),
       -- and rollup("latency") first when spanning several buckets. Storing the sketch
       -- is what makes the percentile correct across buckets instead of an avg of p95s.
       percentile_agg("duration_ms") AS "latency"
FROM "http_requests"
GROUP BY "bucket", "instance", "route", "status"
WITH NO DATA;
-- start_offset is a day, not an hour: the refresh only recomputes buckets Timescale
-- marked invalid, so a wide window costs little when nothing changed but lets a job
-- that failed for a while catch up. A one-hour window would leave a permanent hole.
SELECT add_continuous_aggregate_policy('http_requests_1m',
    start_offset      => INTERVAL '1 day',
    end_offset        => INTERVAL '1 minute',
    schedule_interval => INTERVAL '1 minute',
    if_not_exists     => TRUE);
