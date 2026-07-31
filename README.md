# ShopNexus Server

[![wakatime](https://wakatime.com/badge/github/shopnexus/shopnexus-server.svg)](https://wakatime.com/badge/github/shopnexus/shopnexus-server)

A marketplace backend in Go — a **type-safe distributed system in a monorepo**, orchestrated by [Restate](https://restate.dev) durable execution.

Two goals drive every decision here:

1. **Deploy independently** — each module can run as its own deployment (N instances behind a load balancer) or all together in one binary. Topology is a config choice, not a rewrite.
2. **Keep monolith DX** — cross-module calls stay *type-safe*. `ctrl+click` jumps to the real handler, "find references" shows every caller, the compiler catches a broken signature across module boundaries. None of the proto-drift you get when services only share a contract string.

> Development timeline: [timeline.md](assets/timeline.md)
>
> Code convention: [convention.md](assets/convention.md)

## Why?

### Distributed system, not microservices

These are different axes that people conflate:

- **Microservices** scale *team/org* — independent repos, releases, ownership (Conway's law).
- **Distributed systems** scale *deployment* — separate processes, network boundaries, independent scaling and fault isolation.

This is a solo project, so there's no team to scale — no reason to pay the microservice org-cost (repo sprawl, proto drift, lost type-safety, hand-synced contracts). But the *deployment* benefits are still worth keeping on the table: scale a hot module on its own, isolate failures. So the design target is a **distributed system that keeps a monolith's developer experience** — type-safe calls, one codebase, one `ctrl+click` away from any handler.

### Why a monorepo?

*Many repos are hard to manage.*
> Imagine 100hr+ on configuring things on each repo :D

One repo sidesteps cross-repo dependency-version juggling. The service shape is still there (separate schema per module, calls over the Restate ingress), so promoting a module to its own deployment is a config change, not a refactor.

### Why Restate?

*Orchestration over choreography.*

The flow runs linearly top-to-bottom — easier to debug than tracing events across handlers. In practice it's the message queue between modules: failures retry with backoff (no message dropped, no DLQ needed) and the journal makes those retries durable.

It earns its place from a concrete, present need: **checkout talks to a 3rd-party payment gateway**. You can't hold a DB transaction open while waiting on Stripe/VNPay to resolve — that's true in a monolith too. The moment the flow spans separate commits with rollback-on-failure, you need a **saga**, and durable orchestration is what makes that saga survive crashes and replays.

Restate also gives **location transparency for free**: a caller invokes a service by name and the runtime routes it — same binary or separate deployment, one instance or N behind a load balancer. The call site never changes. That's what makes goal #1 (deploy independently) a config switch.

## Request Flow

Every call goes through a **proxy interface** that mirrors each service's method signatures — callers invoke it as if it were the service itself, while the proxy forwards the request over HTTP to the Restate ingress, which routes it to the target service.

![flow1.jpg](assets/flow1.jpg)

Cross-service calls take the exact same path — Service A never calls Service B directly. Both external traffic and inter-service calls fan in through the proxy and the Restate ingress, so durability, retries, and observability apply uniformly to every call.

![flow2.jpg](assets/flow2.jpg)

The order service depends on `Inventory` as an **interface**, so the call site reads like an ordinary in-process method call — fully type-checked, navigable, refactor-safe:

```go
// Service "order" calling "inventory" through the proxy interface
inventories, err := orderbiz.inventory.ReserveInventory(ctx, inventorybiz.ReserveInventoryParams{
    OrderID: order.ID,
    Items:   items,
})
```

**Two layers of decoupling, kept separate on purpose:**

- *Runtime* decoupling — the call always travels through Restate, so where the callee runs is irrelevant. ✅ done.
- *Compile-time* decoupling — for a module to deploy without compiling its peers' code, each proxy client must live in a leaf `contract` package (types only, no implementation). Today the proxy clients still sit in each module's `biz`, so importing one module's proxy pulls its implementation in. Extracting them is the keystone of the roadmap below.

## Distributed Lock (Redis)

```go
unlock := b.locker.Lock(ctx, "order:123")
defer unlock()
```

Currently I only implement basic Redis lock/unlock, but while working on it I noticed a problem: if the handler takes too long, the lock TTL could expire mid-execution. To handle this, I added a background goroutine that extends the TTL every ttl/2, so long-running handlers never lose the lock. Calling unlock() stops the goroutine and DELs the key.

## Modules

Each module has its own README with ER diagrams, domain concepts, flows, and endpoints.

| Module                                       | Description                                                            |
| -------------------------------------------- | ---------------------------------------------------------------------- |
| [`account`](internal/module/account/)        | Auth, profiles, contacts, favorites, payment methods, notifications    |
| [`catalog`](internal/module/catalog/)        | Products, categories, tags, comments, hybrid search                    |
| [`order`](internal/module/order/)            | Cart, checkout, price negotiation, orders, shipment, refunds           |
| [`inventory`](internal/module/inventory/)    | Stock management, serial tracking, audit history                       |
| [`promotion`](internal/module/promotion/)    | Discounts, ship discounts, scheduling, group-based price stacking      |
| [`analytic`](internal/module/analytic/)      | Interaction tracking, weighted product popularity scoring              |
| [`chat`](internal/module/chat/)              | Messaging, conversations, read receipts                                |

Module boundaries follow DDD bounded contexts: each owns its schema, and cross-module writes go through sagas rather than shared transactions.

[`common`](internal/module/common/) is **not** a module: it has no service and nothing calls it
over an interface. It is the DDL every module's schema gets — `audit_log`, `resource`, `option`,
applied by `cmd/migrate` before that module's own migrations — plus the pgx helpers their
adapters share (`common/dbx`). So an uploaded file belongs to the module that took the upload
and travels with it if that module moves to its own database; in dev every DSN points at the
same server.

## Tools

- **[pgx/v5](https://github.com/jackc/pgx)** as the PostgreSQL driver, wrapped in `pgsqlc.Storage[T]` for connection pooling and transaction support.
- **[SQLC](https://sqlc.dev)** generates type-safe Go structs and query methods from SQL. Config in `sqlc.yaml`. Uses `guregu/null/v6` for nullable types.
- **pgtempl** (`cmd/pgtempl/`) generates SQLC query templates from migration files, producing CRUD queries automatically.
- **genrestate** (`cmd/genrestate/`) generates Restate service definitions and proxy interfaces from Go interface definitions.
- **migrate** (`cmd/migrate/`) manages database migrations. Migration files are in `<module>/db/migrations/` with the format `<version>_<description>.sql`.

## Roadmap

Goal: **independent deployment + type-safe DX**, with topology as a config artifact. In order of leverage:

- **Contract layer** — move each module's Restate proxy client out of `*/biz` into a leaf `internal/module/<m>/contract` package (imports only `<m>/model` + the Restate SDK). This is the keystone: a caller depends on a peer's *contract* without compiling its *implementation* into the binary, and it removes the import-cycle hazard on bidirectional cross-module calls. `genrestate` will emit into `contract/` instead of `biz/`.
- **Topology by config** — replace the hard-coded `Bind()` list in `internal/app/restate.go` with a service table selected by a `SERVICES` env var. `SERVICES=*` runs everything in one binary (today's behavior); `SERVICES=order` runs just order. Same image, many topologies — no per-service `main.go`.
- **Independent deployment on k3s** — each service runs as N pods behind a Kubernetes `Service` (the load balancer); the service registers its k8s Service DNS with Restate, so scaling pods needs no Restate change. Restate routes by name → k8s Service → pods. **Invariant: the marketplace must always still run as a single binary (`SERVICES=*`); splitting is opt-in, enabled only when a real scaling need is measured.**
- **OSS reference template** — this repo, with the marketplace as its worked example, packaged as a type-safe-distributed, deployment-agnostic Go-on-Restate starting point. Not a framework or a product — a reference architecture you can read the *reasons* behind.
