# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

C2C marketplace backend: an HTTP gateway in front of domain modules
(account, catalog, order, finance, chat, trust), plus an `observability`
telemetry module. `common` is not a module and has no service: it is the shared
DDL every module's schema gets (audit_log, resource, option) plus the pgx
helpers their adapters share (`common/dbx`). The `finance` module owns all money primitives
(payment sessions, transaction ledger, wallets, bank accounts, withdrawals) so
escrow moves stay atomic; `trust` owns feedback/reputation/ticket plus product
reviews (review, review_reply, review_vote) and pushes each listing's recomputed average
into catalog's `cached_rating`/`cached_review_count`, because the two live in different schemas
and cannot be joined. Stock lives in `catalog` — there is no
separate inventory module. Product/web analytics is external (Rybbit + ClickHouse). Single Go module
`shopnexus`, Go 1.26, `net/http` ServeMux. Each module is a pragmatic hexagon
that isolates its tables in its own Postgres **schema** (per-module DSN, so a
module can later be split onto its own database); dependencies are wired with
Uber fx.

## Commands

```bash
go build ./...                                   # build everything
go vet ./...                                     # vet
golangci-lint run                                # errcheck + govet + staticcheck + unused (.golangci.yml); must be 0 issues
go test ./...                                    # unit tests (no DB needed)
go test -run '^TestName$' ./internal/module/account/...   # a single test
go test -tags integration ./...                  # adapter/postgres tests (need DBs up + *_DB_DSN set; skip otherwise)
go generate ./...                                # regenerate api/openapi.gen.yaml via cmd/specgen

docker compose up -d                             # infra only: Postgres (timescaledb-ha:pg18) + Redis + NATS/JetStream + Grafana + Loki + Alloy — no host ports
docker compose --profile dev up -d --build       # + gateway with hot reload (air, Dockerfile `dev` target) on :5000
docker compose --profile app up -d --build       # + gateway from the real production image, no hot reload
docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d   # + publish infra ports to the host (host-run gateway, psql, Grafana UI)
docker compose --profile mock up -d --build      # mock API from the spec on :4010 (Prism) — no DB, no Redis, no migrations
docker compose --profile embed up -d --build     # + the embedding worker (EMBEDDING_PROVIDER=mock by default)
go run ./cmd/migrate                             # apply migrations — REQUIRED before first run; app never migrates at startup
docker compose --profile seed run --rm seed      # optional demo data (embedded dataset + drawn photos); refuses to run twice
docker compose --profile seed run --rm seed -wipe -yes-i-mean-it   # ... and the only way to remove it; never runs as part of a load
go run ./cmd/embedder                            # the embedding worker: drains catalog's stale queues on EMBEDDING_INTERVAL
go run ./cmd/embedder -once                      # ... one pass then exit (backfill after a seed/import; non-zero on failure)
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

All configuration is **one YAML document and nothing else** — no environment
variables for any value, no defaults, no `.env`. `internal/config/config.dev.yml`
is it (gitignored, so real credentials live there); `config.example.yml` is the
committed shape, kept loadable by a test. `CONFIG_FILE` points somewhere else,
and *where the file is* is the only thing the environment still decides.
Every field is required, an unknown key fails too (`KnownFields`), and a malformed
one fails at startup naming the path to fix.

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
(`NewValidationError`, `ErrBadRequestBody`, `ErrUnauthorized`, `ErrInvalidToken`, `ErrNotImplemented`);
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

Outside that pipeline, this module also holds `listing_popularity` — a platform-wide,
account-agnostic score, `subscribePopularity`/`subscribePurchases` folding
`catalog.listing_interaction`/`order.placed` into it on the domain bus (their own consumer
groups, independent of `subscribeEvents`' raw mirror on the same topics and of catalog's own
subscriber that feeds personalisation instead). UPDATE-then-INSERT, not the Sink/JetStream/COPY
pipeline above: a score's delta can be negative, and it is a small aggregate table, not a
hypertable of samples. `port.Repository.PopularityOf` reads it back — no `api/` package yet,
since nothing calls it; add one the day something does rather than before.
**Logs** are separate: app logs JSON to stdout → Grafana Alloy (`dev/alloy`)
→ **Loki** → same Grafana. **Product/web analytics is NOT in the backend** —
it's collected client-side by Rybbit (self-hosted, ClickHouse-backed), a
separate stack.

**Layer homes** — `internal/infra/` = technology (postgres pool+Migrate,
`eventbus` pub/sub — Redis Streams for domain events, NATS JetStream for
telemetry — cache), fx-free. `internal/shared/` = cross-cutting helpers (errx,
httpx, logger, token, validation, openapi). `internal/provider/` = external
integrations (finance, transport, llm): an interface + implementations; webhook
routes mount on `*http.ServeMux`, which the router serves **under `api.BasePath`**
like every other route — a callback beside the versioned prefix is a second path
every reverse proxy has to be told about, and the one in front of this platform
was not. `internal/gateway/` = transport (thin handlers
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
  `return X, domain.ErrListingNotFound`, `errx.NewValidationError(...)`, or the
  error from a `domain.NewX` constructor), and re-propagating an error a callee
  already annotated (or a caller-supplied callback's error). `%w` keeps
  `errx.Decompose`/`errors.Is` working through the chain, so HTTP status/code is
  unaffected. In `adapter/postgres` repos, prefix the wrap with `db ` to mark the
  layer (e.g. `fmt.Errorf("db insert account: %w", err)`), so a full chain reads
  `create account: db insert account: <pg error>`.
- **Config is a document, and it is validated where it is written.** Two structs, on purpose: the
  nested `file` in `internal/config/file.go` is the *document* and carries every `validate` tag, so a
  failure names the path somebody edits (`Storage.Secret` is `storage: secret:`); the flat `Config`
  beside it is the *program's* shape, which a hundred call sites read field by field. Keeping them
  apart is what let the document become grouped without those sites learning new paths, and `config()`
  is the one place the two meet. Each seam's selector sits in the same section as the vendor fields it
  governs, so `required_if` is a sibling reference; the one rule a tag cannot express — "required
  because a *list* names this provider" — is a `RegisterStructValidation`, not a hand-rolled check
  outside the validator. A duration is a string (`"15m"`) through a `Duration` type, because yaml.v3
  will not decode one and a config counted in bare seconds is a config nobody can read.
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
- **A seller never approves an order; the money creates it.** Every listing is buyable from its
  page: the buyer checks out a draft that froze the seller's asking price, pays the item plus the
  shipping quote, and the order and its shipment exist as soon as that payment session completes.
  `price_mode` adds a path rather than replacing one — `negotiable` also lets the buyer open a
  negotiation (the chat thread the pair already shares), either side revises the terms, and agreeing
  freezes them for the same checkout; `fixed` refuses to be negotiated, which is now the only thing
  the mode decides. So the only thing a seller can refuse is a price, and there is no route that
  turns paid items into an order: the payment webhook does, which is why `item.order_id` is nullable
  and `item_seller_pending_idx` is a retry list rather than an inbox. The buyer always pays
  delivery, so a seller is never charged and `session_kind` has no `seller-confirmation-fee`.
- **A claim is taken before the money, and the write is the claim.** A draft is spent
  (`WHERE cancelled_at IS NULL`) and an accepted offer moves to `checked-out`
  (`WHERE status = 'accepted'`) *before* anything is reserved or a payment session is opened, so a
  double-clicked checkout opens one session and the loser is refused. Claiming afterwards is the
  bug that shape hides: both presses open a session, only the last write loses, and a second paid
  session on one sale is money the escrow cannot account for — the hold is keyed on the order, so
  the second payment is never held and the payout then fails for ever on a negative balance.
  Whatever fails after the claim hands it back (`ReleaseOfferCheckout`), because a buyer whose
  ledger blinked should retry rather than renegotiate. `item.offer_id` and `item.draft_id` are
  UNIQUE behind all of it, since a constraint holds when a service is wrong.
- **A guarded write names the statuses it moves out of.** `SaveRefund(ctx, r, from)`, `SaveOffer(ctx, o, from)` — `active` for
  a counter, an acceptance or a withdrawal; `active|accepted` for an expiry. A fixed `from` covering
  every status is a lost update wearing a guard: a counter that read the row before somebody else's
  acceptance landed would put terms back on a table that was already agreed, and the buyer holding
  a 200 "agreed" would then be told their checkout has nothing to check out. Same rule as
  `Version` on an aggregate — a stale read has to lose. Naming only the *terminal* statuses is the
  version of this guard that looks right and is not: the overdue-refund sweep reads a batch of 100 and
  works through them one finance call at a time, so a `returned` copy of a row somebody escalated in
  the meantime would settle it — refunding the escrow with no verdict, and leaving a ticket nothing
  can close.
- **Agreeing to a price is not the sale; the buyer's checkout is.** (Nor is negotiating the only
  way to buy a `negotiable` listing — the asking price is takeable, see above.) Either party may
  accept the terms on the table — whoever does *not* own the standing proposal, since the two sides
  alternate — and that charges nothing: it freezes the price for `acceptedWindow` (30 minutes,
  against `offerWindow`'s 12 hours) and the buyer then presses "create order now", which is the
  same checkout a fixed-price listing opens. That split is what makes a *seller* accepting safe:
  no order and no money until the buyer chose a carrier and paid. A lapsed acceptance is
  `Expire()`'s business like any other, and negotiating again is the remedy.
- **Delivery is the buyer's on both paths, and it is quoted, never sent.** `quoteShipping` asks
  the carrier at checkout — from a fixed-price draft and from agreed terms alike — so a client
  cannot decide what carriage costs, and a seller with no pickup contact fails *before* money is
  taken. The session is `goods + fee`; `HoldEscrow` holds only the goods and takes the fee as a
  third `fee` leg in the same movement, because it is the courier's money and a payout must never
  hand it to the seller. It comes back only when the parcel never left: a cancellation returns it
  (`Cancel` has just refused the route if the parcel shipped), a granted refund sends zero —
  carriage that happened was still bought, and who bears the return leg is the verdict's
  business. `POST /shipping-quotes` prices every enabled carrier for a draft **or** an accepted
  offer, one list for both kinds of sale.
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
- **A collected fee has to buy something: the sale books the parcel.** `finishSettlement` calls
  the carrier (`bookShipment`), stores its reference in `transport.data.provider_ref` and treats a
  failure as best-effort — the money has moved, so an unreachable courier is a booking to retry
  (`RetryUnbookedShipments`, on the shared sweeper, guarded by `data->>'provider_ref' IS NULL`)
  rather than an order to refuse. The reference is also the marker: `Booked()` is what keeps a
  retry from posting a second parcel for one sale. The carrier then reports on **its own** id, so
  the provider's webhook is wired in `order/fx.go` and `RecordCarrierCheckpoint` translates that
  vocabulary once (`processing` → `in-transit`); a status this module does not model is ignored
  rather than guessed at, and a late checkpoint loses to `Advance`'s forward-only rule.
- **A timed transition has two drivers and one definition.** Every wait this marketplace
  makes — an unpaid checkout expiring, an escrow window closing, a refund deadline passing, a
  blind rating revealing — is an **idempotent service method**. A Restate run per entity calls
  it promptly (`internal/infra/durable` + each module's `workflow.go`), and `durable.Sweeper`
  calls the same method on `SWEEP_INTERVAL` as the net under a lost run. Neither is a second
  definition of "due", which is what makes leaving the sweep on under Restate free: it finds
  nothing. `WORKFLOW_RUNTIME` (`restate`|`off`) is the selector, same rule as a provider seam,
  and `off` is a real deployment — the sweep is then the only clock. A module reaches the
  runtime through its own `port.Workflows`, every call **best-effort**: the row is already
  committed, so an unreachable runtime is a slower clock rather than a failed request. Restate
  workflow *names* are declared in `port` (`OrderCheckout`, …), because the code that submits a
  run and the code that hosts it must agree and that is the only package both import; a signal
  name is the workflow struct's **method name**, since `restate.Reflect` is what pairs them.
- **Feedback is blind, and the two ratings are counted apart.** `trust.feedback` stays
  invisible (`published_at IS NULL`) until both sides submit or `BlindWindow` passes, so a
  rating cannot be retaliatory — and the *direction is derived* from which side of the order
  the caller is on, never sent. Publishing is what folds a rating into `reputation`, in the
  same transaction, so a visible rating is always a counted one and the `published_at IS NULL`
  guard is what stops a second count. Transaction feedback (`rating_*`) and product reviews
  (`review_rating_*`) are separate column pairs on the same row: one order can produce both,
  and summing them would count that order twice. A `reputation` row is **zeroes, not
  not-found**, for an account nobody has rated.
- **Everything a user raises is a ticket, and one table holds all of them.** `trust.ticket` covers an
  abuse report, a refund dispute, an order issue, a payment problem and a feature request, because
  they are one thing: somebody submitted something and somebody answers. `kind` is the only
  difference — it decides whether the ticket is about something (`RefKindOf`, so `ref_type` follows
  from the kind and is never sent) and whether a report's `reason` is allowed. There is no `report`
  table and no `dispute` table: seven statuses across three tables were one lifecycle written three
  times, and a user asking "where are my requests" had three places to look.
- **The desk is identified by its role, not by its name.** `account.role = 'support'` behind a partial
  unique index, seeded by a migration, is what `GetSupportAccount` looks up — because a *username* is
  something a user can register, and whoever held `support` would otherwise have answered "who is
  support" and become a side of every ticket thread on the platform, reading every complaint. The
  reserved-username guard stays as the second line, and a deployment that never seeded the row fails
  loudly at the first ticket (`ErrSupportAccountMissing`, 500) rather than picking somebody's account.
- **Staff answering a ticket act *as the desk* wherever a value is viewer-relative, and as themselves
  wherever a sender is.** One helper (`chat.Service.side`) decides it once: a moderator is not a side
  of the row, so the counterparty, the read marks and the unread count are computed for the desk —
  which is also what makes the read mark shared, so the next moderator inherits it and the requester's
  receipt says support looked. Message senders are never mapped, or anonymising for the desk would
  blank the requester's own words for the staff reading them. The anonymisation has to happen on
  **every** projection of a message, not just the thread: `last_message` on an inbox row leaked a
  moderator's account id, and `GET /accounts/{id}` needs no token to turn that into a name.
- **Support is reached by raising a ticket, never by messaging the desk.** Its id is public — it is the
  counterparty of every ticket thread — and a direct thread with it is one no moderator can read, so
  `StartConversation` refuses it instead of accepting words nobody will ever see.
- **A ticket's requester-side view is a conversation, so it stores no body and takes no uploads.**
  `POST /tickets`'s `body` and `attachments` become the **first message** of a `kind = 'ticket'`
  thread (`chat.conversation.ticket_id`, UNIQUE), and everything after that is ordinary chat — the
  attachment path, the realtime push and the unread badge already exist. Side B of the thread is the
  **support desk's own account** (`account.username = 'support'`, seeded by a migration, memoised by
  `GetSupportAccount`): that keeps whoever answers anonymous to the requester (`sender_id` blanked
  plus `from_support`), lets the next moderator inherit the thread, and needs no nullable
  participant — the `CHECK a < b` and the read marks hold unchanged. A moderator is let into a
  ticket thread without being a side of it; a direct thread never is.
- **The two rows are in different schemas, so the thread is opened best-effort and repaired on
  read.** The ticket lands first and `OpenTicketThread` is idempotent on `ticket_id`, so a chat
  outage leaves a mute ticket rather than a lost complaint, and `GetTicket` opens the thread the
  next time it is read. Losing the conversation must never lose the ticket.
- **A verdict closes *every* open ticket about the thing it decided.** One open ticket per requester
  per target is what the index holds, and both parties to a refund may escalate, so a lookup that
  answered one row left the other open for ever — and unclosable, since a `refund-dispute` has no hand
  resolution. `OpenTicketsAgainst(refType, refID)` returns the set and the verdict walks it.
- **A verdict that moves money is decided where the money is, and it closes the ticket itself.**
  Opening a `refund-dispute` ticket escalates the refund in order **before** the row is written (so a
  refund order will not escalate produces no queue entry with no possible answer), and staff then
  decide it at `POST /admin/refunds/{id}/verdict`. `POST /admin/tickets/{id}/resolution` answers 409
  for that kind: marking the case settled by hand would leave the escrow where it was. Order
  publishes `RefundResolved` — carrying the deciding moderator, because a verdict has an author —
  and trust records it on the ticket and posts it into the thread, idempotently and quietly for a
  refund nobody raised a ticket about.
- **An operation retried by a sweep needs a marker for "done", not a time window.** The escrow
  release records `order.payout_released_at` when it lands, so `ClaimedPayouts` is *exactly* the
  stranded set: a healthy platform reads nothing, rather than re-asking finance about every sale
  it ever completed. A window instead of a marker is worse than useless — it makes the retry
  cost scale with history and then goes quiet while the debt stands. And a pass that can fail
  for ever logs **one summary line per pass**, not one per row: `escrow releases stranded,
  orders: N` is the thing to alert on, and 1200 identical errors an hour bury whatever else
  happened.
- **A counter that can go down is written with UPDATE-then-INSERT, not an upsert.** Postgres
  checks a constraint against the *proposed* row before it detects the conflict, so
  `INSERT ... ON CONFLICT DO UPDATE` carrying the negative delta of a deleted review fails
  `counters_non_negative` on a row it was never going to write. Update first, insert only if
  nothing was updated, and retry the update on a unique violation (two first-writes racing).
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
- **A DTO sends its zero value; it never drops the key.** No `omitempty`/`omitzero` in any DTO
  json tag — an empty list is `[]`, an empty object `{}`, an unset number `0`, an absent pointer
  `null`. The server marshals with **`encoding/json/v2`** (Go 1.27, no GOEXPERIMENT), which is
  what makes that spellable: v1 rendered a nil slice as `null`, so "none" and "not loaded" were
  one answer and every client collapsed them by hand. A generated client types a spec-`required`
  property as **non-nullable**, so omitting it does not degrade — `Message.refs` carried
  `omitempty`, went missing on the ~all messages that reference nothing, and every chat thread in
  the app failed to deserialise. `TestDTOs_NeverOmitAZeroValue` walks the `api` packages' AST and
  fails naming the field, because the tag is the whole rule and a rule with no check is a rule
  that comes back. Three things stay omitted on purpose: a **provider wire type** (`max_tokens:
  0` or `dimensions: 0` is a broken vendor call, so those keep `omitzero`), an **audit/event
  payload or jsonb params struct** (a diff records what changed, so an untouched field is absent),
  and `httpx.dataEnvelope.Meta` (a single-resource response structurally has no meta).
  `validate:"omitempty,…"` is a different tag and untouched — it means "skip these rules when the
  field is absent", which is exactly right on a PATCH.
  Two more v2 differences bite. `omitempty` now means "encodes as null/`""`/`[]`/`{}`", so it no
  longer drops `0`/`false`; and unmarshal is **case-sensitive**, dropping a mismatched name
  *silently*. So a third party's payload is read through `httpx.DecodeVendorJSON`, which keeps
  v1's lenient matching — eSMS answers `CodeResult` beside `SMSID`, there is no sandbox to
  discover a casing mistake in, and a field that quietly reads as zero is how a settled payment
  becomes an unsettled one. Our own routes use `httpx.DecodeJSON`, strict.
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
- **Email is a channel of a notification, not a second way to tell somebody something.**
  `CreateNotification` writes the feed row *and* sends the mail, because the one place that
  knows an account was told a thing is also the only one holding their address and their
  preference matrix — a mailer subscribing to the bus in parallel would re-derive both and
  need `accountapi` to hand it somebody's email. The channels are decided **independently**:
  silencing the feed is not a way to stop the letters, and `ChannelInApp` returning early in
  front of everything is the bug that shape invites. A mail goes out only when all four hold —
  the caller named a `MailKind`, `domain.Enabled(…, ChannelEmail)`, there is an address, and
  `EmailVerified`. Named rather than derived from `Category`, because a category covers many
  facts and only some have copy; verified-only because an unconfirmed address is a typo or
  somebody else's inbox, and what goes out is a sale and its price. The send is last and
  best-effort: the caller is a bus subscriber, so an error would redeliver the fact and buy a
  duplicate feed row to retry a mail nobody lost.
- **A mail is copy in `templates/mail/`, and the caller sends facts.** One file per kind per
  language (`order-placed.vi.html`), each defining `subject`/`title`/`lead`/`action` and
  optionally overriding `footer`/`extra`; `frame.<lang>.html` owns the layout, the button and
  the chrome, so adding a mail is writing a paragraph rather than copying markup — and there
  is a frame *per language* because the fallback line and the automated-mail notice are copy
  too. `templates` is a package at the repository root beside `api` for a hard reason: a
  `go:embed` pattern cannot name a parent directory, so nothing under `internal/` can reach
  the folder. `notify.Message.Params` carries an order id, a total, a moderator's note —
  never a sentence or a formatted number, since how an amount reads in a locale is the
  template's business (`money` is bound to the set's language). Everything is parsed at
  `NewClient`, and `missingkey=error` makes a parameter the caller forgot a render failure
  instead of a hole in somebody's inbox; the param set is written beside each `Kind` and
  exercised for every kind × language by the smtp package's tests. `account/subscriber.go` is
  the single place a fact is paired with a template, so payload and template stay in sight of
  each other.
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
  `http.Client.Timeout`. Payment and transport are the exception and have **no selector**: see the
  next bullet.
- **Where a past record names the provider, the row picks it — not an env var.** A settled
  `finance.transaction.payment_option` and a shipped `order.item.transport_option` hold an `option`
  slug as plain text for ever, so those two seams are a *registry*, not a selection:
  `common.Registry[T]` holds every implementation the binary has, keyed by the name a row's
  `provider` gives, and `Registry.Client(provider)` is what resolves one. So two rails can be live
  at once, and moving a carrier from GHN to GHTK is `PATCH /admin/options/{id}` — the slug and every
  order naming it stay put — where a deployment-wide selector would have moved all of them at once
  and needed a restart. A provider nobody registered is refused (422), never substituted: charging
  through whichever rail happened to be in the map is worse than failing.
  A provider **declares the rows it owns** (`Client.Options()`), and `SyncOptions` reconciles them
  at startup — upsert plus a delete of what it no longer names — so the list a client picks from is
  the code that will serve it and a scenario cannot outlive its implementation. Answering none means
  "my rows are the operator's business" — a vendor whose rails are a contract this binary cannot
  know; reconciling those would delete what somebody wrote by hand. Where the rail *is* the whole
  product (a SePay bank transfer, a Stripe card) the code declares its one row and owns it.
  `payment.providers`/`transport.providers` are lists of *implementations to register*, not
  selectors: a provider left out is one no row can name, which is how `mock` is kept out of a
  deployment that takes real money, and a mock left in is harmless because nothing charges through it
  unless a row asks for it. Nothing outside a mock package branches on being one.
  Taking a provider out of that list has to take its rows out of the buyer's list too, and nothing
  deletes them — a bad deploy must not cost an operator their configuration — so `common.ListOptions`
  leaves out any row no registered provider serves. Not a mock-shaped special case: it is the same
  rule `Registry.Client` enforces at the till, moved to the one place that would otherwise offer a
  choice whose only outcome is an error. The staff view keeps showing them, since that is where "why
  is this missing from checkout" is answered.
- **A redirect rail sends the payer somewhere and reports on a webhook; the page they land on is not
  evidence.** SePay and Stripe both take `return_url`, both echo the leg id onto it, and both point
  *every* outcome — success, error, cancel — at the same page: where a payer ends up is a claim
  anybody can forge, and the payment session they read on arrival is the fact. Only the callback
  settles a leg. A settle that failed answers 500 so the vendor retries, because that callback is the
  one thing that will tell us again; an event the platform has no opinion on is acked, or it is
  retried for ever. SePay's checkout is opened by the *browser* — its init is POST-only and binds to
  the payer's own session — so that rail serves a self-submitting form of its own and signs its fields
  in a fixed order, which the form's input order has to match: map iteration would build a page it
  refuses. Stripe's callback reads the *payment intent*, not the checkout session: a completed session
  with an unpaid intent is not money, and our leg id rides in the intent's metadata because a vendor
  reports on its own ids. VND is zero-decimal at Stripe, so the amount crosses unscaled.
- **A mock is only worth what its edge cases are, so both of them carry a page for a person.** The
  payment rail's scenarios are the ways a payment goes wrong — declined, unreachable, pending for
  ever, reported twice, reported for another amount — and the courier's are the ways a parcel does:
  a route nobody serves, a quote that hangs, a booking refused after the fee was taken, a parcel that
  goes quiet, a checkpoint delivered twice, one that arrives *behind* where the parcel already is, and
  a status this platform does not model. Each is a rule somewhere else in the codebase — the
  forward-only advance, `RecordCarrierCheckpoint` ignoring a vocabulary it does not know,
  `RetryUnbookedShipments` — and a rule with no way to reach it is a rule nobody checks.
  Both mocks serve HTML: the rail's hosted page decides one payment, the courier's console walks one
  parcel through its checkpoints. That is not decoration. `mock-stuck` used to say "move it along by
  hand with POST", which meant a curl command nobody ran, so the scenario existed and was never
  exercised. Neither swallows a failed report either: a hand-driven checkpoint that did not land
  answers 500, because one that logged and replied 200 looked exactly like one that did.
- **One `/options` endpoint over every category, and the rows stay per module.** `category` says who
  answers (`payment` → finance, `transport` → order), because those two columns must be able to move
  databases with their module; the projection, the DTOs and the staff gate live once in
  `common/optionapi.go`. `common.CategoryVisibleTo` decides who may see a category, and one nobody
  defined answers the same 404 as one a user may not — telling them apart enumerates the operator
  surface. Absent is 400, which is a different mistake. `/admin/options` adds the disabled rows, each
  row's `provider`, and the set an admin may switch it to; a switch-off keeps the row resolvable for
  the records naming it, which is what the soft delete and the immutable slug are for.
- **The stale mark is the embedding queue, and only the worker clears it.** Three catalog tables
  carry `embedding_stale_at` — listing, category, tag — set by whatever write changed what the row
  *says*; `cmd/embedder` drains them into `*_embedding` and clears the mark. A work list that is a
  property of the data rather than a message somebody has to not lose: a row edited during a
  deploy is still stale afterwards, and a pass that already ran finds nothing. The clear is
  `WHERE embedding_stale_at = @the_value_that_was_read`, the same guarded-write idiom as a
  versioned aggregate, so a row re-marked while the model was working stays queued instead of
  being silently declared fresh. Both halves land in one transaction with the vector, so a row
  that left the queue always has one. Its own binary, not a `durable.Sweep`: a pass is a batch of
  model inferences and the process serving requests should not hold that — and not running it at
  all is a supported deployment, since search falls back to trigram. `EMBEDDING_DIMENSIONS` is
  checked against every answer because the model service lives in another repository: a model of
  the wrong width does not degrade, every row fails until a migration changes the column.
- **A store's selector picks where the next write goes, not what can be read.** Storage is the
  one seam whose past choices outlive the current one: a `resource` row records the provider
  holding it, so `STORAGE_PROVIDER` names only `storage.Registry`'s **write** store while every
  store ever used stays readable, resolved per row. Reading through the preferred store instead
  is worse than an error — a store signs whatever key it is handed, so an object it has never
  held still comes back as a well-formed link and the failure surfaces in the browser. `remote`
  is a real provider on that list (the object key *is* an https URL, so there is nothing to sign
  and no credential to configure, which is why it needs no selector), and it refuses every
  write, since only `Registry.Write()` ever takes one. `cmd/seed` used to point its photos at
  it — an https URL into a marketplace's CDN — and now writes CC0/public-domain photographs it
  carries in `cmd/seed/photos/` into `local` instead (credits in `photos/ATTRIBUTION.md`, drawn
  placeholders where no free photograph of the object exists): the hotlink was a third party's
  copyright and a dependency the other side could switch off, and it did, which is why every
  seeded gallery was empty.
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

- **A value that crosses a package boundary is a named constant, published by the side that owns
  it.** A module's `api` package is where the other modules read it, so that is where it lives:
  `accountapi.RoleAdmin`, `orderapi.RoleSeller`/`StateOpen`, `catalogapi.PriceModeFixed`,
  `trustapi.RoleSeller`, `common.ChangeTypeUpdate`, `finance.KindBuyerCheckout` (re-exported from
  its domain at the level a subscriber imports). Its `domain` twin stays — `domain` may not import
  `api` — and that is the one duplication the layering forces; five modules each spelling
  `"moderator"` was not. Same rule below the modules: a provider package names the selector value
  that picks it (`fptai.Name`, `local.Name`, `esms.Name`) and `internal/config` names the runtimes
  it validates (`config.WorkflowRestate`), so the composition root compares instead of retyping.
  A `validate:"oneof=..."` tag is the one place a literal is unavoidable — a struct tag cannot
  reference a constant.
- **A guard's SQL is built from the constant that sets the value.** `WHERE status = '` +
  `domain.OfferAccepted` + `'` stays a compile-time constant string, and a renamed status becomes a
  build failure instead of an UPDATE that matches nothing. Worth it exactly where the write depends
  on it: the claims, the expiry lists, the terminal-status sets.
- **A constructor with more than a few same-typed arguments takes a struct.** `domain.NewItem(NewLine{…})`
  and `domain.NewOffer(NewTerms{…}, window)`: twelve positional arguments with four ids in a row —
  or seven consecutive `int64`s — transpose without a compile error and produce a valid-looking sale.
  Named fields also let a field the caller does not know yet (an item's payment session) simply not
  be in the constructor.
- **Three copies is a home; one caller is not a seam.** `dbx.NullText`, `common.FormatCursor`/
  `ParseCursor`/`ErrCursorInvalid`, one `cursorMeta`, one `page[T]` tail, one `Deps`: each replaced
  three-to-six identical copies, and the cursor one replaced three separators for the same format.
  The other direction is just as real — a helper, an interface method or a config hook with a single
  caller is deleted or inlined (`provider.Option`, `payment.Refund`, `cache.Config`), because a
  seam nobody has crossed twice is a guess about the future.
- **A comment says why; the signature already says what.** Cut anything that restates the code, and
  keep the invariant, the failure it prevents, and the decision behind it. A count in prose ("the
  three workflows") is a comment that goes stale on the next commit — say what the thing is instead.

- **The AI fills a form; the seller posts it.** `POST /listings/suggestions` reads the photos a seller
  just uploaded plus what they said — typed, or a voice note transcribed server-side — and answers a
  filled-in `CreateListingRequest`-shaped suggestion. It **writes nothing**: no listing, no draft, no
  row for an attempt that was abandoned, and `POST /listings` is still the only way a listing comes
  into existence. A route that posted for them would make a model the author of claims about somebody
  else's goods — its condition, its price — which is not a claim this marketplace can stand behind.
  So: one synchronous call (the client shows a skeleton form), every field optional except `name`,
  a value the route cannot stand behind comes back **empty** rather than guessed, `price` is only ever
  a number the seller said out loud, and the `category` the model answers is a *name copied from the
  list in the prompt* — resolved against the real tree, because an id is a token a model invents.
  The transcript is echoed so a seller can see why a field is wrong instead of guessing.
- **A model reads bytes, not links.** `storage.Client.Fetch` exists because the objects here are
  behind signed URLs only this gateway serves, which a hosted model cannot follow — so a photo travels
  inside the request as a data URI (`llm.Message.Images`). `remote` refuses to be read: its keys are
  somebody else's origin and this platform does not proxy them.
- **A shopper's action is a published fact, and three independent readers fold it into three
  different things.** `POST /listings/interactions` publishes `catalog.listing_interaction`
  (`catalog/event.go`) for six client-observed kinds — `view`, three `click-from-*`, and
  `not-interested`/`hidden` — validated against exactly that set (`catalogapi.InteractionWeight`'s
  keys, the one place the vocabulary is named). `order.OrderPlaced` carries a seventh,
  `purchase`, never client-submitted: it is derived from a completed sale, so it stays out of the
  route's validated set but shares the same weight map and `catalogapi.PositiveInteractionTypes`.
  Nobody calls anybody synchronously — catalog's own subscriber turns the four positive kinds plus
  `purchase` into `listing_signal` rows (append-only, next to `favorite`, both read by
  `interestSignals`); observability's separate subscriber (own consumer group, same topic — a
  third reads the raw copy into `business_events`) folds every kind into `listing_popularity`,
  UPDATE-then-INSERT because a weight can be negative. `not-interested`/`hidden` never enter
  `interestSignals`' positive-weighted average — an average that becomes a page's share cannot
  hold a negative number — they instead exclude a listing outright in `recommendedWhere`,
  checked live at request time rather than baked into the precomputed vector.

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
