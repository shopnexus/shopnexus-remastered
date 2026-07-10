# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

Architecture: see [`README.md`](README.md). Module layout, nullable types, error patterns: see [`assets/convention.md`](assets/convention.md).

Each module (`internal/module/<name>/`) has its own README with ER diagram + flows.

## Commands

See `Makefile`. Common:

- `make dev` — air hot-reload
- `make pgtempl` — regen SQLC query templates
- `make generate` — regen Restate proxies (`*/biz/restate_gen.go`)
- `make openapi` — regen OpenAPI spec (`internal/openapi/`) from handler annotations
- `make migrate` / `make seed` — apply / seed dev DB
- `go test ./...` — tests
- `golangci-lint run` — lint (config: `.golangci.yml`)

## Do not hand-edit generated files

`internal/module/*/db/queries/generated_queries.sql`, `internal/module/*/db/sqlc/*.sql.go`, `*/biz/restate_gen.go`, `internal/openapi/*`. Fix the generator (`cmd/pgtempl/`, `sqlc.yaml`, `cmd/genrestate/`) instead.

## OpenAPI / Swagger (code-first)

The HTTP gateway spec is generated from swaggo annotations on the `transport/echo` handlers — `make openapi` runs `swag init` (Swagger 2.0 → `internal/openapi/swagger.{json,yaml}`) then `cmd/openapiconv` (→ OpenAPI 3.0 `openapi.v3.json`, the artifact Mintlify consumes). Swagger UI is served at `/swagger/index.html`. CI (`make openapi-check`) fails on drift — rerun `make openapi` and commit after changing any handler signature/annotation.

- Envelope: annotate as `response.CommonResponse{data=pkg.T}`; paginated endpoints use the non-generic `response.SwaggerPaginationResponse{data=[]pkg.T}` (swaggo can't resolve a generic type-arg whose package the transport file doesn't import).
- Non-stdlib field types (guregu/null, uuid, `Null<Enum>`, `json.RawMessage`) are mapped to scalars in `.swaggo`; add new ones there.

## Dev DB

Do not `DROP SCHEMA ... CASCADE` or `migrate -down` without explicit approval. `make seed` is slow.

## Migration format (`internal/module/*/db/migrations/*.up.sql`)

- 1-line field comments trail the column; multi-line / section comments lead.
- Inside `CREATE TABLE`: PK/UNIQUE first, blank line, then FKs. No `-- Constraints` / `-- Foreign keys` headers.
- FK column type must exactly match the referenced column (`BIGSERIAL` → `BIGINT`, not `UUID`).
- `ON DELETE SET NULL` only on nullable columns; use `NO ACTION` for `NOT NULL`.
- Cross-module FKs (`account_id`, `sku_id`, `buyer_id`, `payment_option`) are NOT declared — modules stay decoupled at the DB layer.

## Saga (`internal/shared/saga`)

- Declare `var err error` once at function top. Use `=` everywhere — `:=` (including `if err := f(); err != nil`) shadows the outer var and breaks `defer if err != nil { sagaTx.Compensate() }`.
- `saga.Defer` callback: `func(restate.Context) error`. Cross-module proxy calls (`b.account.*`, `b.inventory.*`, …) run bare. Raw `Querier()` / side effects self-wrap in `restate.RunVoid`.

## Idempotency (per-module ledger tables)

- Ledger row and side effect in the **same tx, same schema**. No global / cross-module idempotency table.
- Forward path: `INSERT ... ON CONFLICT DO NOTHING` — duplicate key → return terminal error (fail loud).
- Compensator: `DELETE ... RETURNING` — missing key → no-op success.

## Biz handlers (query vs command)

- **Query** = pure read → `ctx context.Context`, flat args. No journaling.
- **Command** = side effect → `ctx restate.Context`, journaled. Structure in 3 phases, marked with comments:
  - `// decision:` read + validate inside `restate.Run` (no commit). Fail-fast UX only — NOT a correctness guard.
  - `// execution:` the durable commit(s). Cross-module `Call` self-journals; raw DB writes wrap in `restate.Run`/`RunVoid`.
  - `// tail:` post-commit fan-out (notify, analytics, workflow signals).
- One 3-phase set per scope. A loop with per-item side effects → extract the body to a helper carrying its own decision/execution/tail (e.g. `rejectForBuyer`); never nest a 2nd decision in the outer scope. Multiple steps in one phase → number them `1.`/`2.`/`3.`, don't repeat the phase header.

## State transitions (TOCTOU)

- Never validate-then-act across a journal boundary. The check in `decision` is stale by `execution` (separate tx, gap may span a payment-wait suspend; no row lock survives suspend).
- Guard at the **write**: conditional `UPDATE ... WHERE <expected state>`, use `:execrows`, check rows-affected == expected, else abort + compensate.
- Enforce invariants in the DB (partial unique index, e.g. `refund_one_active_per_order`), not app-level read-check.
