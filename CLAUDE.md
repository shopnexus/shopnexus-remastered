# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

Architecture: see [`README.md`](README.md). Module layout, nullable types, error patterns: see [`assets/convention.md`](assets/convention.md).

Each module (`internal/module/<name>/`) has its own README with ER diagram + flows.

## Commands

See `Makefile`. Common:
- `make dev` — air hot-reload
- `make pgtempl` — regen SQLC query templates
- `make generate` — regen Restate proxies (`*/biz/restate_gen.go`)
- `make migrate` / `make seed` — apply / seed dev DB
- `go test ./...` — tests
- `golangci-lint run` — lint (config: `.golangci.yml`)

## Do not hand-edit generated files

`internal/module/*/db/queries/generated_queries.sql`, `internal/module/*/db/sqlc/*.sql.go`, `*/biz/restate_gen.go`. Fix the generator (`cmd/pgtempl/`, `sqlc.yaml`, `cmd/genrestate/`) instead.

## Dev DB — no destructive resets

Do not `DROP SCHEMA ... CASCADE` or `migrate -down` without explicit approval. `make seed` is slow. Prefer additive migrations.

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
