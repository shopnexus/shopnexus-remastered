# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

C2C marketplace backend: an HTTP gateway in front of domain modules
(account, catalog, order, finance, chat, trust), plus an `observability`
telemetry module. `common` is not a module and has no service: it is the shared
DDL every module's schema gets (audit_log, resource, option) plus the pgx
helpers their adapters share (`common/dbx`). The `finance` module owns all money primitives
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

docker compose up -d                             # infra only: Postgres (timescaledb-ha:pg18) + Redis + NATS/JetStream + Grafana + Loki + Alloy — no host ports
docker compose --profile dev up -d --build       # + gateway with hot reload (air, Dockerfile `dev` target) on :5000
docker compose --profile app up -d --build       # + gateway from the real production image, no hot reload
docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d   # + publish infra ports to the host (host-run gateway, psql, Grafana UI)
docker compose --profile mock up -d --build      # mock API from the spec on :4010 (Prism) — no DB, no Redis, no migrations
go run ./cmd/migrate                             # apply migrations — REQUIRED before first run; app never migrates at startup
go run ./cmd/gateway                             # run the gateway (needs all env vars — see README.md)

go run ./cmd/mockspec /tmp/mock.yaml && npx -y @stoplight/prism-cli@5 mock -p 4010 /tmp/mock.yaml   # the same mock without Docker
```

**Three dev modes, pick by what you are doing.** `--profile dev` and
`--profile app` are alternatives — both publish host port 5000, so they are not
meant to run together.

| Mode | Use when | Cost |
|---|---|---|
| infra only + `go run ./cmd/gateway` on the host | normal Go iteration | fastest (native build cache); logs go to your terminal, not Loki |
| `--profile dev` | you need logs in Loki, or to exercise observability | ~4s rebuild on save; matches prod libc/paths |
| `--profile app` | last check before pushing | builds the real `runtime` image; no hot reload |

`Dockerfile` has three stages: `build`, `dev` (air, bind-mounted source) and
`runtime` (distroless). **CI builds `runtime` explicitly** — never rely on "last
stage wins", or a stage appended later ships the Go toolchain.

All config env vars are **required, no defaults** (`internal/config`); a missing
one fails fast at startup.

Every route is served under **`api.BasePath`** (`/api/v1`) — the router registers
paths unprefixed and mounts the mux there, and `openapi.base.yaml`'s
`servers[0].url` must match it (a contract test fails if they drift). So Swagger UI
is at `/api/v1/docs` and the raw spec at `/api/v1/openapi.yaml`.

## Architecture (the parts that span files)

**Module layout** — every `internal/module/<name>/` is identical in shape:
- `domain/` — entities + pure business rules + **all** module errors in `domain/errors.go` (not-found *and* app-level, e.g. `ErrAccountNotFound`, `ErrEmailTaken`). Imports only stdlib + `shared/errx` + `shared/validation`.
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

**OpenAPI** — the spec is authored as **one fragment per aggregate**,
`internal/module/<module>/api/openapi/<aggregate>.yaml`, merged by `cmd/specgen`
into `api/openapi.gen.yaml` (embedded + served + published). Regenerate with
`go generate ./...`. `internal/gateway/openapi_contract_test.go` guards that the
served spec is valid. Rules for a fragment: only `paths` and
`components.schemas` are merged (anything reusable across modules —
parameters, responses, security schemes — belongs in `api/openapi.base.yaml`);
schema and path keys share **one flat namespace** across every module, so a
duplicate key fails the merge; a schema lives with the aggregate that owns it
even when another module refs it. The module's admin/moderator surface is one
`admin.yaml` per module, and the module-level prose (what it covers, its
boundaries) sits at the top of its **root** aggregate's file — `account.yaml`,
`listing.yaml`, `conversation.yaml`, `resource.yaml`, `payment-session.yaml`,
`order.yaml`, `feedback.yaml`.

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
  `option.id` are `VARCHAR` slugs, never encoded), and a table whose id the
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
- **An optional domain field is a pointer — always, mechanically.** If the column is
  nullable, the Go field is `*T`; there is no judgment call about whether the type's
  zero could pass for "not set", because that call has to be made again by every
  reader. `nil` is the one representation of absent, so a `NULL` column scans straight
  into the field and writes straight back — the adapter has no `nullText`/`nullID` and
  the SELECT has no `COALESCE`, and a `*string` DTO field is a direct assignment rather
  than a conversion.
- **Pointer handling is written out, not routed through a helper package.** There is no
  `optional`/`patch` package: `if s := NormalizeEmail(email); s != "" { a.Email = &s }`
  says what it does at the site, where a generic `NonZero` made the reader jump. So —
  building an optional field is `if v != "" { x = &v }`; reading one is
  `if p != nil { … *p … }` (or a plain `*p` where a guard just proved it non-nil, with the
  guard named in a comment); comparing two is `a != nil && b != nil && *a == *b`; and a
  pointer to a value is the **`new(expr)` builtin** — Go 1.26 lets `new` take an expression, so
  `new(time.Now())` and `new("draft")` replace both a one-use local and the `ptr[T]` helper every
  package used to redeclare. A local variable stays where the value is also read (`next :=
  NormalizeEmail(email)` then `&next`). No double pointer anywhere: `**T` as a parameter or a
  field is not a shape this codebase has.
- **A domain mutator takes the value, not the pointer, and clearing is its own method.**
  `SetEmail(string)` / `ClearEmail()` beats `ChangeEmail(*string)`: the domain then holds no
  pointer logic at all, and the service reads
  `switch { case req.ClearEmail: acc.ClearEmail(); case req.Email != nil: acc.SetEmail(*req.Email) }`.
  Passing `""` is the same fact as clearing, so there stays exactly one way to spell absent.
- **Persistence:** pgx `pgx.NamedArgs` + hand-written SQL. No ORM, no sqlc.
  `FindBy*` returns `(zero, domain.ErrXNotFound)` — never `(nil, nil)` sentinels
  across the port.
- **An entity is a child of an aggregate only when a rule spans it and the root.**
  Apply the test, do not assume a table is a child because it has the account's id:
  "at least one way to sign in" spans `account.password` and the linked providers, so
  `oauth_identity` is a child; a push token identifies an install and moves between
  accounts, so `device` is its own aggregate. Pulling every table under one root is the
  legacy mistake in a new shape — a display-name change should not load a hypertable.
  Today the only aggregate is `account` = {`account`, `oauth_identity[]`}; everything else
  in the module — `contact`, `device`, `identity_document` — is its own.
- **A mandatory 1-1 table is columns on the parent, not a table.** `profile` was merged into
  `account`: a display name is required, inserted in the same statement, read by every
  command and written by the same UPDATE, so the split bought a join and a second write and
  nothing else. `domain.Profile` survives as a value object over those columns — the DTO
  shape is unchanged and `FindProfile` still answers just the display half. A 1-1 table
  earns its keep only when the halves are written or read apart (a hypertable, an optional
  detail row nobody loads on the common path).
  `contact` is the worked example of the test failing: one-default-per-kind spans the
  contact set only, never a field of the account row, so pulling it in would load
  eighteen columns of address on every display-name change.
- **A child is an exported field, mutated directly.** `domain.Account` holds
  `Profile` and `Identities []*OAuthIdentity` in the open; a caller assigns
  `acc.Profile.Name` and appends. Three things make that safe and they are one package
  deal: `Get` is the **only** loader (so a slice is always the whole set), `Save`
  **validates the whole aggregate** (so a broken invariant is refused at the write), and
  `Save` **deletes by negation** (`provider <> ALL(@keep)`, so no removal list has to be
  kept and forgetting to record one is not a failure mode that exists). Break one and the
  other two stop holding. A method is written only for what an assignment cannot do: a
  rule only the root sees (`Unlink`), a change with a consequence (`SetEmail` clears
  `email_verified`), a fact worth recording.
- **A command is `Get` → mutate in memory → `Save`, guarded by `Version`.** No callback,
  no write-surface interface: `Save` validates, writes the row `WHERE version = @version`,
  syncs the children and commits, or answers `domain.ErrVersionConflict` (409) — a stale
  read loses instead of overwriting what it never saw. That is what stops two concurrent
  unlinks of different providers from both reading "there is another way in". Retry is the
  client's call; add a retry loop in a service only when a route needs it.
  The conditional-write idiom (`UPDATE ... WHERE status = 'pending'`, check
  `RowsAffected`) stays for a guard with no aggregate to version — `identity_document`.
- **A guard that reads before it writes needs a lock, not just a subquery.** `NOT EXISTS` in
  the `WHERE` of an UPDATE is a read: under READ COMMITTED two concurrent statements each see a
  world where their own write is legal, so both land (write skew). `UpdateCategory` takes
  `pg_advisory_xact_lock(categoryTreeLock)` first, which serialises re-parents against each
  other and nothing else — cheaper than SERIALIZABLE, which would push a 40001 retry loop into
  every service. A recursive walk in such a guard uses `UNION`, never `UNION ALL`: with
  `UNION ALL` one cycle that did get in makes the statement non-terminating, so the route that
  would repair it hangs too.
- **A DB constraint that already enforces a rule stays.** The aggregate does not replace a
  partial unique index: `contact`'s one-default-per-kind is cleared by the adapter in the
  same transaction as the write, because the index holds even when a service is wrong.
- **A domain event is a fact, never an instruction.** `domain.Event{Code, Payload}` is what
  a mutator recorded (`account.email_changed`, `account.suspend`); `Save` turns each into
  an `audit_log` row **in the same transaction as the change**, so a write that landed
  always has a trail and the diff comes from the decision instead of a reconstruction.
  The test that keeps the line: **delete every `record()` call and the database is still
  right** — only the trail is lost. Persistence is driven by the struct's state, never by
  the event list, because "no event" must never mean "no write". `Happened(code)` is how a
  service asks "did this command change the email" without snapshotting a field first — asked
  **before** `Save`, which clears the events once they are audited.
  An insert has no version to guard and no events yet, so its one audit row is still
  written by hand (`InsertAuditLog`).
- **What every module shares lives in `common`, and `common` is not a module.** It has no
  api, no service and nothing calls it over an interface: a module imports what it needs.
  `common/migrations` is DDL that `cmd/migrate` applies into **every** module's schema before
  that module's own — `audit_log`, `resource`, `option` — so the text exists once and the table
  once per schema, and a module can still take its rows to its own database. Seven modules used
  to carry a hand-copied `audit_log` and four of those copies had already drifted.
  `common/dbx` is the pgx layer: `InTx`, `SQLState`/`IsUniqueViolation`/`IsNoRows`/…,
  `JSONObject`/`Int64Array`/`NullTime`/`NullID`/`NullJSON`, `InsertAuditLog`, and the
  `Resources`/`Options` stores a module builds on its own pool. `isUniqueViolation` had four
  definitions and `jsonObject` two before this. `common` itself imports only the standard
  library, `shared/errx`, `shared/id` and `shared/validation`, so a `port` or an `api` package
  can name `common.Resource` or `common.AuditEntry` without pulling pgx behind it.
- **A resource id only resolves inside the module that stored it.** The upload belongs to the
  module that took it — an avatar to account, a listing photo to catalog — so a DTO carrying an
  attachment carries the resolved `common.ResourceDTO`, never a bare id for the client to go
  fetch from somewhere. There is no module-agnostic `POST /resources`: each module grows its own
  upload route when it implements that flow.
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
  `awaiting-buyer-action`, `awaiting-seller-review`.
- **A seller never approves an order; the money creates it.** `price_mode` is the only thing
  that differs between the two ways a sale starts. `fixed` is bought from the listing page: the
  buyer checks out a draft, pays the item plus the shipping quote, and the order and its shipment
  exist as soon as that payment session completes. `negotiable` cannot be checked out — the buyer
  opens a negotiation, which is the chat thread the pair already shares, either side revises the
  terms, and the buyer accepting opens the same checkout. So the only thing a seller can refuse
  is a price, and there is no route that turns paid items into an order: the payment webhook
  does, which is why `item.order_id` is nullable and `item_seller_pending_idx` is a retry list
  rather than an inbox. The buyer always pays delivery, so a seller is never charged and
  `session_kind` has no `seller-confirmation-fee`.
- **An order records which of the two it came from.** `order` and `item` each carry a nullable
  `draft_id` and a nullable `offer_id` with `CHECK ((draft_id IS NOT NULL) <> (offer_id IS NOT
  NULL))`, and both are UNIQUE — so a webhook delivered twice or an acceptance double-clicked
  cannot mint a second order, and "where did this sale come from" is answered by the row instead
  of a join. A negotiated sale has no draft: the accepted offer is what froze its terms.
- **A negotiation is order's row and chat's thread.** `order.offer` holds the terms, the status
  and the expiry, because they decide money and one-active-per-`(buyer, variant)` is a partial
  unique index a hypertable cannot hold. Chat carries `{"offer_id": N}` in a system message's
  metadata and nothing else — copying the price into the message would let a counter-offer leave
  the thread showing terms that are no longer on the table. Chat already has one thread per pair
  of accounts, so there is nothing to create and no id to pass around.
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
- **Sessions, not just tokens (`shared/session` + `shared/token`).** An access
  token is a 15-minute JWT naming the account (subject, opaque id) *and* its
  session (`jti`); the session itself is a Redis key with a 30-day TTL, and
  `middleware.Auth` looks it up on **every** authenticated request. That one
  lookup is what makes a logout, a password change or a suspension effective
  against a token already in circulation. A refresh token is a second key
  pointing at the same session and is rotated on every exchange. Revoking every
  session of an account is an **epoch bump** (`session-epoch:<id>`), not a list
  walk, so it stays O(1) and needs no set type in `cache.Client`; a session
  record carries the epoch it was born with. Both TTLs are constructor arguments
  in `cmd/gateway`, like the JWT TTL always was.
- **A tri-state PATCH field is a pointer plus a `clear_*` bool.** Absent leaves the field
  alone, a value replaces it, the flag removes it — three states out of two ordinary JSON
  fields, no custom unmarshaller and no `null` on the wire. A **required** column gets no
  flag, because there is nothing to clear (`if req.X != nil { dst = *req.X }`); a nullable
  one gets one (`switch { case req.ClearX: dst = nil; case req.X != nil: dst = req.X }`).
  Fields that only make sense together share a single flag — `clear_district`,
  `clear_location` — but are still **applied one field at a time**, or half a pair reaches
  the entity as two set values and the "both or neither" rule can no longer refuse it. The
  service applies the patch onto the entity and then validates the **whole** entity, so
  "not the last identifier" or "a birth date is not in the future" is checked against the
  result rather than the field. Never drop a clear to protect a required field: that answers
  200 to a request the service did not carry out.
- **An event binds its code to its payload type, like `eventbus.Topic[T]` does for the bus.**
  `domain/events.go` declares one var per fact —
  `var EmailChanged = newEventType[IdentifierChange]("account.email_changed")` — and nothing
  else names that string. Recording is the generic free function `record(a, EmailChanged,
  IdentifierChange{…})` (Go has no generic methods), reading is
  `PayloadOf(e, EmailChanged)`, which answers false for a different fact so nobody reads the
  wrong shape out of an `any`. A payload is a **struct with json tags**, never
  `map[string]any`: those tags are `audit_log.diff`'s column names, so changing one rewrites
  how history reads. Same for a row dump — `Account.Snapshot()` returns `AccountSnapshot`.
  The point is the pair being declared once: a typo'd literal used to compile and silently
  match nothing, and a map payload made every reader guess its keys.
- **A repository write is static SQL, never built by string concatenation.** After a
  `Get`/`Find` the caller holds the whole row, so the statement is a plain
  `SET col = @col` list. Where a patch is applied without reading first, it is still one
  constant string: `COALESCE(@x, x)` for a NOT NULL column, and
  `CASE WHEN @set_x THEN @x ELSE x END` for a nullable one, where the pair on the params
  struct is the same `*T` + `SetX bool` the DTO carries. A cross-field rule ("changing the
  phone clears `phone_verified`") disqualifies the blind patch: writing it in SQL moves the
  rule out of `domain`, where no test reaches it without a database.
- **One-time secrets live in Redis, not in a table** (email verification,
  password reset, contact phone code): each is read once and then has to
  disappear, which is a TTL rather than a row somebody sweeps. Same for send
  throttles, and a throttle key is set **before** the account lookup so a 429
  cannot be used to tell an existing address from an unknown one.
- **Request plumbing is shared (`gateway/handler/params.go`):** `actor`,
  `pathID[K]`, `pageParams`/`limitParam`, `boolParam`, `decodeBody`/`check`. A
  handler reads the request, fills in what only the gateway knows, calls the
  service, writes the result — and a limit that means 20 on one route and 50 on
  another is a bug a client finds in production.
- **Role checks belong to the service, not the handler.** The caller's role is a
  row in the account module's table, so a handler could only learn it by asking
  that service anyway; `/admin/*` handlers stay as thin as the rest, and an admin
  passes every moderator check.
- **A module's `api` package ships a test stub** (`api/accounttest`, like
  `shared/id/idtest`) once the contract grows past a handful of methods: embed it
  and override the one method under test. It answers 501, so an unstubbed call is
  an obviously wrong status rather than a plausible zero value.
- **A provider seam is chosen by config, never by code.** Each one has a required
  selector env var (`EMAIL_PROVIDER`, `SMS_PROVIDER`, `OAUTH_VERIFIER`,
  `KYC_PROVIDER`) with `mock` as one of the choices, and that vendor's credentials
  are `required_if` the selector picked it — the only conditional in
  `internal/config`, and still not a default: an unknown selector fails at startup
  rather than falling back, because a deployment that thinks it sends real email
  and does not is discovered by the user who never got their reset link. Today:
  SMTP (`net/smtp`, vendor-neutral) for email, eSMS.vn for SMS, OIDC id-token
  verification (`coreos/go-oidc`) for federated sign-in, FPT.AI eKYC for identity.
  Every real client is built with `httpx.ObserveOutbound` and no
  `http.Client.Timeout`.
- **Vendor-shaped differences stay behind the seam.** `kyc.Client.Check` covers
  both a vendor that reads the scans and answers now (FPT.AI: a decided `Result`)
  and one that runs its own web flow (Sumsub-style: pending plus a session URL);
  the caller stores whichever came back. A verdict from a vendor goes through the
  same `domain` transitions a moderator's does, so an automated check cannot skip
  the rule that a passport needs an expiry or a rejection needs a reason.
- **A message template that can silently drop its payload is validated at
  startup** (`esms`: rendering a probe code must contain it). A misconfiguration
  where every send succeeds and every user is stuck is the expensive kind.
- **The spec is also the mock, so a schema carries bounds and an example.**
  `docker compose --profile mock up -d --build` serves the whole contract from
  `api/openapi.gen.yaml` on `:4010` (Prism), which is how a client gets written
  against a route before its handler exists — it needs no database and validates
  the request and the bearer requirement, so a bad body still gets the real error
  envelope. What that mock answers *is* the spec: an unbounded integer mocks as
  `-9007199254740991` and a plain string as `"string"`. So every numeric field
  gets `minimum`/`maximum` where one is true (a signed delta gets neither — it
  gets an example instead), and every field a client renders gets an `example`.
  Both are contract claims, so they must be claims the service actually keeps:
  bounds mirror the DTO's `validate` tag (`gt=0` → `minimum: 1`). `cmd/mockspec`
  exists only because Prism mounts `paths` as written and ignores the relative
  `servers[0].url`, so a mock would otherwise answer `/listings` while the gateway
  answers `/api/v1/listings`.

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
5. Add `api/openapi/<aggregate>.yaml` — one per aggregate, plus `admin.yaml` if it
   has a moderator surface; run `go generate ./...`.
