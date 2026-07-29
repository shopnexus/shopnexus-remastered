# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

C2C marketplace backend: an HTTP gateway in front of domain modules
(account, catalog, order, finance, chat, trust), a `common` schema for
cross-module shared tables (resource, option), plus an
`observability` telemetry module. The `finance` module owns all money primitives
(payment sessions, transaction ledger, wallets, bank accounts, withdrawals) so
escrow moves stay atomic; `trust` owns feedback/reputation/report plus product
reviews (review, review_reply, review_vote). Stock lives in `catalog` — there is no
separate inventory module. Product/web analytics is external (Rybbit + ClickHouse). Single Go module
`shopnexus`, Go 1.26, `net/http` ServeMux. Each module is a pragmatic hexagon
that isolates its tables in its own Postgres **schema** (per-module DSN, so a
module can later be split onto its own database); dependencies are wired with
Uber fx.

## Commands

```bash
go build ./...                                   # build everything
go vet ./...                                     # vet
go test ./...                                    # unit tests (no DB needed)
go test -run '^TestName$' ./internal/module/account/...   # a single test
go test -tags integration ./...                  # adapter/postgres tests (need DBs up + *_DB_DSN set; skip otherwise)
go generate ./...                                # regenerate api/openapi.gen.yaml via cmd/specgen

docker compose up -d                             # infra: Postgres (timescaledb-ha:pg18) + Redis + NATS/JetStream + Grafana + Loki + Alloy — no host ports
docker compose --profile app up -d --build       # also run gateway+migrate as containers so their logs ship to Loki
docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d   # + publish infra ports to the host (host-run gateway, psql, Grafana UI)
go run ./cmd/migrate                             # apply migrations — REQUIRED before first run; app never migrates at startup
go run ./cmd/gateway                             # run the gateway (needs all env vars — see README.md)
```

All config env vars are **required, no defaults** (`internal/config`); a missing
one fails fast at startup.

Every route is served under **`api.BasePath`** (`/api/v1`) — the router registers
paths unprefixed and mounts the mux there, and `openapi.base.yaml`'s
`servers[0].url` must match it (a contract test fails if they drift). So Swagger UI
is at `/api/v1/docs` and the raw spec at `/api/v1/openapi.yaml`.

## Architecture (the parts that span files)

**Module layout** — every `internal/module/<name>/` is identical in shape:
- `domain/` — entities + pure business rules + **all** module errors in `domain/errors.go` (not-found *and* app-level, e.g. `ErrAccountNotFound`, `ErrEmailTaken`). Imports only stdlib + `shared/errx`.
- `api/` (package `<name>api`) — the **published contract**: `Service` interface + request/response DTOs with `validate` tags. Imports **only `context`** — never pgx/http/fx/validator. Other modules and the gateway depend on this, not on the service package.
- `port/` — `Repository` interface the adapter must satisfy.
- `adapter/postgres/` — `Repository` impl using pgx `pgx.NamedArgs` + hand-written SQL. Its `_test.go` is `//go:build integration`.
- `service.go` — implements `<name>api.Service`; the only place orchestrating domain + repo. `migrations/` — embedded SQL, exposed as `Migrations() fs.FS`.
- `fx.go` — `var Module = fx.Module(...)` providing the repo (`fx.As(new(port.Repository))`) and service (`fx.As(new(<name>api.Service))`).

**Dependency injection (Uber fx)** — one `fx.go` per module. `cmd/gateway`
composes: base providers (config, logger, token, validator, bus, cache) + every
`<module>.Module` + `gateway.Module` (handlers, router, HTTP server lifecycle),
then `fx.New(...).Run()`. Cross-module wiring is automatic by interface type:
e.g. catalog's service consumes `accountapi.Service`, order's consumes
`bus.Client`, catalog's consumes `cache.Client`. Each `newRepo` opens its own
pool and registers an fx `OnStop` to close it.

**Error model (`internal/shared/errx`)** — errors are *coded*: HTTP status +
stable `code` + message. Build them with `errx.NewError(status, code, msg)`
(static) or `errx.NewErrorf(status, code, "%s").Fmt(args...)` (template).
`httpx.WriteError` calls `errx.Decompose` to render `{"error":{"code","message"}}`
with the right status — it is the only place that maps errors to HTTP. errx
wraps a Restate terminal error so a failure survives a Restate hop and is not
retried, while `errors.As` still reaches the code same-process. `errx` holds
**only** the common cross-cutting errors used across many modules/handlers
(`ErrValidation`, `ErrBadRequestBody`, `ErrUnauthorized`, `ErrInvalidToken`);
every module-specific error — not-found and app-level alike — lives in that
module's `domain/errors.go` (so the adapter can return it and imports stay one-way).

**OpenAPI** — the spec is authored as per-module YAML fragments merged by
`cmd/specgen` into `api/openapi.gen.yaml` (embedded + served + published).
Regenerate with `go generate ./...`. `internal/gateway/openapi_contract_test.go`
guards that the served spec is valid.

**Observability** — the `observability` module is cross-cutting operational
telemetry. It follows the module layout (`domain/`, `port/`,
`adapter/postgres/` with the `COPY` inserts, `migrations/`, `fx.go`) minus `api/`:
nothing calls it, it is driven by the middleware, the sampler and the bus. It is a
two-stage pipeline: `Sink` **publishes** each sample to **NATS JetStream**
(`eventbus.NATS`), and `subscribeWriter` **consumes** each telemetry topic with a
batch size + linger, handing whole batches to `port.Repository`, which `COPY`s
them into its TimescaleDB hypertable on the `observability` schema. Two buses live
in the graph, told apart **by type**: telemetry takes the concrete
`*eventbus.NATS`, domain events take the `eventbus.Client` interface (Redis) —
using the interface for telemetry silently wires the writer to the wrong bus.
Four signals:
`http_requests` (inbound RED, captured by `Sink.Middleware` wrapping the
router), `provider_calls` (outbound RED, captured by `httpx.ObserveOutbound` in
a provider's `http.Client`; `Sink.OutboundObserver()` is the adapter),
`runtime_metrics` (Go runtime sampler), `business_events` (domain-bus subscriber
mirroring `order.placed`, …). Every row carries `instance` (env `INSTANCE_ID`,
required, stamped once by the `Sink`) so replicas stay separable, and
`http_requests_1m` keeps a `percentile_agg` sketch — read p95 with
`approx_percentile(0.95, "latency")`, never by averaging p95s. Grafana reads the
tables directly (Postgres datasource, provisioned from `dev/grafana`). No Prometheus.
Publishing is async/best-effort — never block or fail a request on telemetry; a
sample that cannot reach the bus is counted (`reportDropped`) rather than
retried. Once a sample *is* in JetStream it is durable: a failed insert nacks the
batch and is redelivered, so a database blip no longer loses telemetry.
**Logs** are separate: app logs JSON to stdout → Grafana Alloy (`dev/alloy`)
→ **Loki** → same Grafana. **Product/web analytics is NOT in the backend** —
it's collected client-side by Rybbit (self-hosted, ClickHouse-backed), a
separate stack.

**Layer homes** — `internal/infra/` = technology (postgres pool+Migrate,
`eventbus` pub/sub — Redis Streams for domain events, NATS JetStream for
telemetry — cache), fx-free. `internal/shared/` = cross-cutting helpers (errx,
httpx, logger, token, validation, openapi). `internal/provider/` = external
integrations (finance, transport, llm): an interface + implementations; webhook
routes mount on `*http.ServeMux`. `internal/gateway/` = transport (thin handlers
→ `api.Service`, middleware, `gwctx` for request-scoped userID/requestID).

## Conventions

New rules get appended here as concise bullets; when a topic outgrows a bullet,
give it its own doc under `docs/` and link it from here.

- **KISS.** Prefer the simplest thing that works. No premature abstraction, no
  speculative generality (YAGNI). Add a layer/interface only when a second
  concrete caller or a real seam needs it.
- **Dependency direction:** `adapter → port → domain`. `domain` never imports
  pgx/http/fx. The `api` package imports only `context` and `shared/id` (validate
  tags are plain strings, so no validator import).
- **Keys are `BIGINT` in the database and `int64` in Go.** Every surrogate PK is
  `"id" BIGINT GENERATED ALWAYS AS IDENTITY` and every referencing column is
  `BIGINT`. Two exceptions: a **natural** key stays what it is (`catalog.tag.id`,
  `common.option.id` are `VARCHAR` slugs, never encoded), and a table whose id the
  app must know before the INSERT uses `GENERATED BY DEFAULT AS IDENTITY` plus
  `nextval(pg_get_serial_sequence(...))` (`finance.payment_session`,
  `finance.transaction`). No `UUID` keys, no second "human-readable number"
  sequence beside an id.
- **Opaque ids on the wire (`shared/id`).** `domain`, `port` and
  `adapter/postgres` handle raw `int64`; the published `api` DTO field is
  `id.ID[id.Listing]`, which marshals to `"lst_2h9qk4mfx7bd3"` — a keyed Feistel
  permutation over the whole int64 range, base32 Crockford, prefix per entity.
  Convert only at the DTO edge (`id.Of[K](n)`, `req.ID.Int64()`,
  `id.Parse[K](r.PathValue("id"))`), and use `validate:"required"` (never `uuid`).
  The zero id is JSON `null` and SQL `NULL`. A polymorphic `ref_id` keeps a
  `string` DTO field and is resolved with `id.ParseOpaque(prefixFor(refType), s)`.
  Adding an entity = one `Kind` struct in `shared/id/kinds.go`; a prefix and the
  `ID_CIPHER_KEY` are **permanent**, since changing either invalidates every id
  already published. The codec is a package global set once by `id.SetCipher` at
  startup — the one sanctioned global, because `json.Marshaler` has no seam for a
  dependency. Design: `docs/superpowers/specs/2026-07-28-proxy-id-design.md`.
- **Persistence:** pgx `pgx.NamedArgs` + hand-written SQL. No ORM, no sqlc.
  `FindBy*` returns `(zero, domain.ErrXNotFound)` — never `(nil, nil)` sentinels
  across the port.
- **Schema isolation:** every module lives in a Postgres schema named after it.
  Each module's pool sets `search_path = <schema>, public` (in `newRepo` via
  `postgres.NewPool(dsn, schema)`), so **all SQL — DDL and queries — stays
  unqualified** (`CREATE TABLE account`, `INSERT INTO account`). Table names are singular.
  `public` in the path keeps shared extensions (pgvector, `pg_trgm`) resolvable. Never write
  `schema.table` in migrations or repos. Only `cmd/migrate` names the schema —
  it runs `CREATE SCHEMA IF NOT EXISTS <module>` before applying that module's
  migrations. All modules may share one DB (dev) or split later via their DSN.
- **Quoted identifiers in migration DDL:** in `migrations/*.sql`, every
  schema-owned identifier is double-quoted — tables, columns, constraints,
  indexes, enum types (both at `CREATE TYPE "status"` and where a column uses
  it: `"status" "status" NOT NULL`), functions, and views. Quote them in
  expressions too (`CHECK ("amount" > 0)`, `WHERE "status" = 'active'`,
  index columns). Left unquoted: SQL keywords, built-in and extension type
  names (`UUID`, `TIMESTAMPTZ`, `vector(768)`), operator classes
  (`gin_trgm_ops`), function *calls* to non-module functions
  (`gen_random_uuid()`, `now()`, `create_hypertable('t', 'ts', …)` — its table
  argument is a string literal), and `CREATE EXTENSION` names. Quoting is
  case-sensitive, so identifiers stay `snake_case` and quoted names must match
  exactly. This is a migration-DDL rule only — repo SQL keeps its current
  unquoted style. Still never schema-qualify (see above).
- **Error wrapping:** never return a bare propagated `err`. Wrap it at the call
  site with `fmt.Errorf("<the operation that failed>: %w", err)` describing the
  *callee's* action (e.g. `"save listing: %w"`, not the caller's job). The only
  exceptions: returning a coded domain/errx error value directly (e.g.
  `return X, domain.ErrListingNotFound`, `errx.ErrValidation.Fmt(...)`, or the
  error from a `domain.NewX` constructor), and re-propagating an error a callee
  already annotated (or a caller-supplied callback's error). `%w` keeps
  `errx.Decompose`/`errors.Is` working through the chain, so HTTP status/code is
  unaffected. In `adapter/postgres` repos, prefix the wrap with `db ` to mark the
  layer (e.g. `fmt.Errorf("db insert account: %w", err)`), so a full chain reads
  `create account: db insert account: <pg error>`.
- **Config:** every env var `required`, no defaults/fallback.
- **Logging:** structured **JSON** `*slog.Logger` to **stdout** (`shared/logger`,
  built with a `service` attr), injected via constructor — never a package
  global. Stdout is the shipping contract: Grafana Alloy tails container logs
  into **Loki**; don't add file/network log sinks in the app.
- **Comments and identifiers:** English.
- **Enum values:** lowercase `kebab-case` for every enum-like string value —
  Postgres enum labels (`CREATE TYPE ... AS ENUM ('awaiting-seller-review')`)
  *and* app-layer `TEXT`/`VARCHAR` columns used as enums (e.g. payment
  `kind` = `'buyer-checkout'`, resource `provider` = `'minio'`). SQL
  *identifiers* (schema/table/column/type/constraint names) stay `snake_case`;
  only the stored value strings are kebab. Multi-word: `product-spu`,
  `awaiting-buyer-action`, `seller-confirmation-fee`.
- **Migrations:** embedded per module, applied only by `cmd/migrate` (a
  CI/CD/init step) — never at app startup.
- **Tests:** table/behavior tests with fakes for services (no DB); real DB only
  in `//go:build integration` adapter tests that skip when the DSN is unset.
- **Service-to-service:** depend on the other module's `api.Service` interface
  (fx-injected), never its concrete package.
- **infra/shared stay fx-free:** their fx providers live at the app level
  (`cmd/gateway`) or in a module's `fx.go`, not inside the package.
- **Outbound deadlines:** every provider client applies its own per-operation
  `context.WithTimeout` (durations are required `Config` fields — no defaults,
  same rule as env config), because timeout length is provider knowledge. Never
  use `http.Client.Timeout`: it covers reading the body, so it truncates streams.
  Streaming gets its own, longer budget covering the whole read (see
  `provider/llm/litellm`). `context.WithTimeout` already keeps the caller's
  deadline when it is shorter, so a request-scoped budget always wins.
- **Outbound cross-cutting concerns live in the transport,** not in per-method
  wrappers or generated decorators: metrics today (`httpx.ObserveOutbound`), and
  retry/circuit breaking when needed, all as `http.RoundTripper` layers on a
  provider's `http.Client`. One layer covers every method of every provider, and
  a `RoundTrip` returns at the response *headers* — which is the right health
  signal for a stream (time-to-first-byte), not the full generation time.
  Semantic behaviour that needs domain knowledge (degrade `Rerank` to a no-op,
  pick another payment option) stays a hand-written decorator on the provider's
  interface. Timeouts are the exception, per the bullet above: the transport
  cannot tell a stream from a one-shot call.

## Commits

Conventional style, lowercase, one line, no body, no trailers:
`type: short imperative subject` where type ∈ `feat|fix|refactor|chore|docs`.
Examples: `feat: redis bus for order events`, `refactor: module/infra/shared layout`.

## Adding a new module (the repeatable pattern)

1. `internal/module/<name>/` with `domain/` (incl. `domain/errors.go`), `api/`
   (package `<name>api`), `port/`, `adapter/postgres/`, `service.go`,
   `migrations/` + `Migrations() fs.FS`, and `fx.go` exposing `Module`.
2. Add its DSN to `internal/config` (required) and its DB to `docker-compose.yml`.
3. Register `<name>.Module` in `cmd/gateway` and its migration target in `cmd/migrate`.
4. Add gateway handler(s) in `internal/gateway/handler`, routes in `router.go`,
   and provide the handler in `internal/gateway/fx.go`.
5. Add the module's OpenAPI fragment; run `go generate ./...`.
