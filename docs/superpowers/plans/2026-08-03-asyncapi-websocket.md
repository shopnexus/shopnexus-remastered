# AsyncAPI + WebSocket Realtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Push chat, offer, order and notification facts to the browser over an authenticated WebSocket, described by a generated AsyncAPI 3.0 document built from per-module fragments.

**Architecture:** A service calls `realtime.Notify`, which publishes a `{code, at, data}` envelope on core NATS pub/sub subject `ws.acct.<accountID>`. Every gateway replica's `Hub` subscribes that subject only while it holds a socket for that account, so NATS filters server-side. The socket is receive-only and authenticated by a single-use Redis ticket, because a browser cannot set `Authorization` on `new WebSocket()`.

**Tech Stack:** Go 1.26, `github.com/coder/websocket`, NATS core pub/sub (not JetStream), Redis (rueidis), Uber fx, `net/http` ServeMux; Next.js 16 + `@tanstack/react-query` on the website.

**Spec:** `docs/superpowers/specs/2026-08-03-asyncapi-websocket-design.md`

## Global Constraints

- Go 1.26, single module `shopnexus`. `golangci-lint run` must report **0 issues** (errcheck + govet + staticcheck + unused).
- **Every config env var is required, no defaults.** A missing one fails fast at startup (`internal/config`).
- **Error wrapping:** never return a bare propagated `err`. Wrap with `fmt.Errorf("<callee's action>: %w", err)`. In `adapter/postgres`, prefix with `db ` (e.g. `"db insert notification: %w"`). Exceptions: returning a coded `domain`/`errx` error value directly, or re-propagating an error a callee already annotated.
- **Dependency direction:** `adapter → port → domain`. `domain` never imports pgx/http/fx. A module's `api` package imports only `context` and `shared/id`.
- **Keys are `BIGINT` in the database and `int64` in Go.** Opaque ids on the wire via `shared/id`; convert only at the DTO edge.
- **An optional field is a pointer, mechanically.** Nullable column → `*T`. Build with `if v != "" { x = &v }`, or `new(expr)` (Go 1.26 allows `new` of an expression).
- **Enum values are lowercase `kebab-case`** in Postgres labels and in app-layer TEXT columns. SQL identifiers stay `snake_case`.
- **Quoted identifiers in migration DDL:** every schema-owned identifier double-quoted. Never schema-qualify.
- **`infra` and `shared` stay fx-free.** Their fx providers live in `cmd/gateway` or a module's `fx.go`.
- **Logging:** structured JSON `*slog.Logger` injected via constructor, never a package global.
- **Comments and identifiers in English.** A comment says *why*; the signature says what.
- **Commits:** conventional, lowercase, one line, no body, no trailers. `type: short imperative subject`, type ∈ `feat|fix|refactor|chore|docs`.
- **Tests:** table/behavior tests with fakes for services (no DB). Real DB/Redis/NATS only in `//go:build integration` tests that skip when the DSN is unset.
- Work on branch `feat/asyncapi-websocket` (already created; the spec commit is `694005ce`).
- `docs/` is in `.gitignore` — plan and spec files need `git add -f`.

---

## File Structure

**Phase A — notification producer (independent of WebSockets, ships first)**

| File | Responsibility |
|---|---|
| `internal/module/account/domain/notification.go` | + `NewNotification` constructor with validation |
| `internal/module/account/port/port.go` | + `InsertNotification` |
| `internal/module/account/adapter/postgres/notification.go` | + the INSERT |
| `internal/module/account/service_notification.go` | + `CreateNotification` honouring `Preference` |
| `internal/module/account/api/api.go` | + `CreateNotificationRequest`, `Notification` already exists |
| `internal/module/account/subscriber.go` | Redis subscriber: `order.placed` / `order.settled` → notification row |

**Phase B1 — the specification pipeline**

| File | Responsibility |
|---|---|
| `internal/shared/specmerge/specmerge.go` | YAML-tree merge primitives + `FindRoot`; owns the duplicate-key invariant |
| `internal/shared/openapi/merge.go` | keeps only OpenAPI document shape; delegates to `specmerge` |
| `internal/shared/asyncapi/merge.go` | AsyncAPI document shape + transitive schema closure copy from the OpenAPI doc |
| `api/asyncapi.base.yaml` | server, the single channel, `ws` bindings, envelope-agnostic shell |
| `internal/module/chat/api/asyncapi/message.yaml` | the four chat messages |
| `cmd/specgen/main.go` | writes both `openapi.gen.yaml` and `asyncapi.gen.yaml` |
| `internal/shared/openapi/handler.go` | + `AsyncAPISpecHandler` serving the embedded document |
| `internal/gateway/asyncapi_contract_test.go` | document validity + Go↔spec code drift guard |

**Phase B2 — transport primitives**

| File | Responsibility |
|---|---|
| `internal/infra/cache/cache.go` + `redis.go` + `memory.go` | + `GetDel`, atomic read-and-delete |
| `internal/shared/session/ticket.go` | `Tickets`: `Issue` / `Redeem` |
| `internal/infra/eventbus/fanout.go` | `Broadcast` / `OnBroadcast` on `*NATS` via core pub/sub |
| `internal/shared/realtime/realtime.go` | `Fanout` seam, `Event[T]`, `Notify`, `Envelope`, subject scheme |

**Phase B3 — the gateway**

| File | Responsibility |
|---|---|
| `internal/gateway/ws/hub.go` | `Hub`, `Client`, subject lifecycle, backpressure policy |
| `internal/gateway/ws/hub_test.go` | fan-out, drop-on-full, cleanup, subject cancellation |
| `internal/gateway/handler/ws.go` | `POST /ws/tickets`, `GET /ws`, the write pump |
| `internal/gateway/router.go` | mount both; `/ws` outside the auth chain and outside RED metrics |
| `internal/gateway/fx.go` | provide `Hub` + `WS` handler |
| `internal/config/config.go` | six required env vars |

**Phase B4 — producers**

| File | Responsibility |
|---|---|
| `internal/module/chat/event.go` | four `realtime.Event[T]` declarations |
| `internal/module/chat/service.go` | `Notify` the *other* participant after each write |
| `internal/module/order/event.go` | + `OfferUpdated` |
| `internal/gateway/bridge.go` | Redis `order.placed`/`order.settled` → `realtime.Notify` for buyer and seller |
| `internal/module/order/api/asyncapi/offer.yaml`, `order.yaml` | fragments |
| `internal/module/account/api/asyncapi/notification.yaml` | fragment |

**Phase B5 — website**

| File | Responsibility |
|---|---|
| `openapi-ts.config.ts` | point `input` at the sibling checkout; delete the drifted copy |
| `scripts/gen-ws-events.mjs` | AsyncAPI → discriminated union |
| `src/realtime/client.ts` | ticket, connect, jittered backoff reconnect |
| `src/realtime/handlers.ts` | event → cache operation |
| `src/realtime/RealtimeProvider.tsx` | one connection per app |

---

## Phase A — Notification Producer

### Task 1: `domain.NewNotification`

**Files:**
- Modify: `internal/module/account/domain/notification.go`
- Test: `internal/module/account/domain/notification_test.go`

**Interfaces:**
- Consumes: `domain.Notification`, `domain.Category` (already exist, `notification.go:37`, `:6`)
- Produces: `domain.NewNotification(NewNotificationParams) (Notification, error)`; `domain.ErrNotificationInvalid`

- [ ] **Step 1: Write the failing test**

Create `internal/module/account/domain/notification_test.go`:

```go
package domain_test

import (
	"errors"
	"testing"
	"time"

	"shopnexus/internal/module/account/domain"
)

func TestNewNotification(t *testing.T) {
	tests := []struct {
		name   string
		params domain.NewNotificationParams
		want   error
	}{
		{
			name: "valid",
			params: domain.NewNotificationParams{
				AccountID: 42,
				Category:  domain.CategoryOrder,
				Title:     "Your order shipped",
				Payload:   map[string]any{"order_id": "ord_x"},
			},
		},
		{
			name: "account required",
			params: domain.NewNotificationParams{
				Category: domain.CategoryOrder,
				Title:    "t",
			},
			want: domain.ErrNotificationInvalid,
		},
		{
			name: "title required",
			params: domain.NewNotificationParams{
				AccountID: 42,
				Category:  domain.CategoryOrder,
			},
			want: domain.ErrNotificationInvalid,
		},
		{
			name: "category must be known",
			params: domain.NewNotificationParams{
				AccountID: 42,
				Category:  domain.Category("gossip"),
				Title:     "t",
			},
			want: domain.ErrNotificationInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domain.NewNotification(tt.params)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if tt.want != nil {
				return
			}
			if got.AccountID != tt.params.AccountID {
				t.Errorf("AccountID = %d, want %d", got.AccountID, tt.params.AccountID)
			}
			if got.CreatedAt.IsZero() {
				t.Error("CreatedAt is zero; the constructor stamps it")
			}
			if got.ReadAt != nil {
				t.Error("ReadAt should be nil on a fresh notification")
			}
		})
	}
}

// A scheduled notification is not yet delivered, so it must not read as unread now.
func TestNewNotificationScheduled(t *testing.T) {
	at := time.Now().Add(time.Hour)
	got, err := domain.NewNotification(domain.NewNotificationParams{
		AccountID:   7,
		Category:    domain.CategorySystem,
		Title:       "Maintenance",
		ScheduledAt: &at,
	})
	if err != nil {
		t.Fatalf("NewNotification: %v", err)
	}
	if got.ScheduledAt == nil || !got.ScheduledAt.Equal(at) {
		t.Fatalf("ScheduledAt = %v, want %v", got.ScheduledAt, at)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -run '^TestNewNotification' ./internal/module/account/domain/`
Expected: FAIL — `undefined: domain.NewNotificationParams`, `undefined: domain.NewNotification`, `undefined: domain.ErrNotificationInvalid`

- [ ] **Step 3: Add the error to `domain/errors.go`**

All module errors live in `domain/errors.go`, not beside the type. Append:

```go
// ErrNotificationInvalid covers every way a notification fails its own rules. The
// producer is backend code, never a client, so the reasons are not worth
// distinguishing on the wire.
var ErrNotificationInvalid = errx.NewError(http.StatusBadRequest, "notification_invalid", "notification is not valid")
```

- [ ] **Step 4: Write the constructor**

Append to `internal/module/account/domain/notification.go`:

```go
// NewNotificationParams is a struct rather than positional arguments: four of the
// fields are strings or maps and would transpose without a compile error.
type NewNotificationParams struct {
	AccountID int64
	Category  Category
	Title     string
	Payload   map[string]any
	// ScheduledAt is a future dispatch time; nil means it goes out now.
	ScheduledAt *time.Time
}

// NewNotification validates a notification and stamps its creation instant.
func NewNotification(p NewNotificationParams) (Notification, error) {
	if p.AccountID == 0 || p.Title == "" || !validCategory(p.Category) {
		return Notification{}, ErrNotificationInvalid
	}
	return Notification{
		AccountID:   p.AccountID,
		Category:    p.Category,
		Title:       p.Title,
		Payload:     p.Payload,
		CreatedAt:   time.Now(),
		ScheduledAt: p.ScheduledAt,
	}, nil
}

func validCategory(c Category) bool {
	for _, known := range Categories {
		if known == c {
			return true
		}
	}
	return false
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -run '^TestNewNotification' ./internal/module/account/domain/`
Expected: PASS (5 subtests + the scheduled case)

- [ ] **Step 6: Vet and lint**

Run: `go vet ./internal/module/account/... && golangci-lint run ./internal/module/account/...`
Expected: no output

- [ ] **Step 7: Commit**

```bash
git add internal/module/account/domain/notification.go internal/module/account/domain/notification_test.go internal/module/account/domain/errors.go
git commit -m "feat: notification constructor with validation"
```

---

### Task 2: Persist a notification

**Files:**
- Modify: `internal/module/account/port/port.go:108-110`
- Modify: `internal/module/account/adapter/postgres/notification.go`
- Test: `internal/module/account/adapter/postgres/repo_integration_test.go`

**Interfaces:**
- Consumes: `domain.NewNotification` (Task 1)
- Produces: `port.Repository.InsertNotification(ctx context.Context, n domain.Notification) (int64, error)` — returns the generated id

- [ ] **Step 1: Add the port method**

In `internal/module/account/port/port.go`, beside the three existing notification methods:

```go
	// InsertNotification writes one feed row and answers its generated id. The
	// caller has already decided the account wants it in-app: preference is a
	// service rule, not a storage one.
	InsertNotification(ctx context.Context, n domain.Notification) (int64, error)
```

- [ ] **Step 2: Write the failing integration test**

Append to `internal/module/account/adapter/postgres/repo_integration_test.go` (the file already carries `//go:build integration` and a `newTestRepo` helper — reuse them):

```go
func TestInsertNotification(t *testing.T) {
	repo, accountID := newTestRepo(t), seedAccount(t)

	n, err := domain.NewNotification(domain.NewNotificationParams{
		AccountID: accountID,
		Category:  domain.CategoryOrder,
		Title:     "Your order shipped",
		Payload:   map[string]any{"order_id": "ord_x"},
	})
	if err != nil {
		t.Fatalf("NewNotification: %v", err)
	}

	id, err := repo.InsertNotification(t.Context(), n)
	if err != nil {
		t.Fatalf("InsertNotification: %v", err)
	}
	if id == 0 {
		t.Fatal("InsertNotification returned id 0")
	}

	rows, err := repo.ListNotifications(t.Context(), port.NotificationQuery{AccountID: accountID, Limit: 10})
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Title != "Your order shipped" {
		t.Errorf("Title = %q", rows[0].Title)
	}
	if rows[0].Payload["order_id"] != "ord_x" {
		t.Errorf("Payload = %v, want order_id=ord_x", rows[0].Payload)
	}
	if rows[0].ReadAt != nil {
		t.Error("a fresh row must be unread")
	}

	unread, err := repo.CountUnreadNotifications(t.Context(), accountID)
	if err != nil {
		t.Fatalf("CountUnreadNotifications: %v", err)
	}
	if unread != 1 {
		t.Errorf("unread = %d, want 1", unread)
	}
}
```

If `seedAccount` does not already exist in that file, add it next to `newTestRepo`:

```go
// seedAccount inserts the minimum account a foreign key will accept and returns its id.
func seedAccount(t *testing.T) int64 {
	t.Helper()
	var id int64
	err := testPool.QueryRow(t.Context(),
		`INSERT INTO account (email, password_hash, name) VALUES (@email, 'x', 'Test')
		 RETURNING id`,
		pgx.NamedArgs{"email": "ws-" + uuid.NewString() + "@example.test"},
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	return id
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test -tags integration -run '^TestInsertNotification$' ./internal/module/account/adapter/postgres/`
Expected: FAIL — the repo does not implement `InsertNotification`

(If it SKIPs, the DSN is unset. Bring infra up with `docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d`, run `go run ./cmd/migrate`, and export `ACCOUNT_DB_DSN`.)

- [ ] **Step 4: Implement the insert**

Append to `internal/module/account/adapter/postgres/notification.go`. Note the unqualified table name — the pool's `search_path` is the module schema — and `dbx.JSONObject`/`dbx.NullTime`, which every adapter here already uses:

```go
func (r *Repository) InsertNotification(ctx context.Context, n domain.Notification) (int64, error) {
	const q = `
		INSERT INTO notification (account_id, category, title, payload, created_at, scheduled_at)
		VALUES (@account_id, @category, @title, @payload, @created_at, @scheduled_at)
		RETURNING id`

	var id int64
	err := r.pool.QueryRow(ctx, q, pgx.NamedArgs{
		"account_id":   n.AccountID,
		"category":     string(n.Category),
		"title":        n.Title,
		"payload":      dbx.JSONObject(n.Payload),
		"created_at":   n.CreatedAt,
		"scheduled_at": dbx.NullTime(n.ScheduledAt),
	}).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("db insert notification: %w", err)
	}
	return id, nil
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test -tags integration -run '^TestInsertNotification$' ./internal/module/account/adapter/postgres/`
Expected: PASS

- [ ] **Step 6: Confirm nothing else broke**

Run: `go build ./... && go test ./... && golangci-lint run`
Expected: build clean, unit tests pass, 0 lint issues

`go build` is the check that matters here: adding a method to `port.Repository` breaks every fake that implements it. Fix `internal/module/account/fake_repo_test.go` by adding:

```go
func (f *fakeRepo) InsertNotification(_ context.Context, n domain.Notification) (int64, error) {
	f.notifications = append(f.notifications, n)
	return int64(len(f.notifications)), nil
}
```

and a `notifications []domain.Notification` field on `fakeRepo`.

- [ ] **Step 7: Commit**

```bash
git add internal/module/account/port/port.go internal/module/account/adapter/postgres/notification.go internal/module/account/adapter/postgres/repo_integration_test.go internal/module/account/fake_repo_test.go
git commit -m "feat: insert notification rows"
```

---

### Task 3: `CreateNotification` and the order subscriber

**Files:**
- Modify: `internal/module/account/api/api.go`
- Modify: `internal/module/account/service_notification.go`
- Create: `internal/module/account/subscriber.go`
- Modify: `internal/module/account/fx.go`
- Test: `internal/module/account/service_notification_test.go`

**Interfaces:**
- Consumes: `port.Repository.InsertNotification` (Task 2), `domain.NewNotification` (Task 1), `order.OrderPlacedTopic` / `order.OrderSettledTopic` (`internal/module/order/event.go:26`, `:46`)
- Produces: `accountapi.Service.CreateNotification(ctx, accountapi.CreateNotificationRequest) (accountapi.Notification, error)`; `account.SubscribeOrderEvents(bus eventbus.Client, svc accountapi.Service, log *slog.Logger)`

- [ ] **Step 1: Add the request DTO**

In `internal/module/account/api/api.go`, beside the other notification DTOs. The `api` package imports only `context` and `shared/id`, so `Category` is a plain string with a `validate` tag — a struct tag cannot reference a constant, which is the one place a literal is unavoidable:

```go
// CreateNotificationRequest is backend-to-backend: no route exposes it, because a
// client must not be able to write another account's feed. It reaches the service
// from a bus subscriber.
type CreateNotificationRequest struct {
	AccountID id.ID[id.Account] `json:"account_id" validate:"required"`
	Category  string            `json:"category" validate:"required,oneof=order promotion system chat social"`
	Title     string            `json:"title" validate:"required,max=200"`
	Payload   map[string]any    `json:"payload,omitempty"`
}
```

Add to the `Service` interface in the same file:

```go
	CreateNotification(ctx context.Context, req CreateNotificationRequest) (Notification, error)
```

- [ ] **Step 2: Write the failing service test**

Create `internal/module/account/service_notification_test.go`:

```go
package account_test

import (
	"errors"
	"testing"

	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/shared/id"
)

func TestCreateNotificationWritesInApp(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(t, repo)

	got, err := svc.CreateNotification(t.Context(), accountapi.CreateNotificationRequest{
		AccountID: id.Of[id.Account](42),
		Category:  string(domain.CategoryOrder),
		Title:     "Your order shipped",
		Payload:   map[string]any{"order_id": "ord_x"},
	})
	if err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}
	if got.Title != "Your order shipped" {
		t.Errorf("Title = %q", got.Title)
	}
	if len(repo.notifications) != 1 {
		t.Fatalf("wrote %d rows, want 1", len(repo.notifications))
	}
	if repo.notifications[0].AccountID != 42 {
		t.Errorf("AccountID = %d, want 42", repo.notifications[0].AccountID)
	}
}

// The in-app channel is a preference like any other: turning it off means no row,
// not a hidden row, because the feed has no notion of invisible entries.
func TestCreateNotificationRespectsPreference(t *testing.T) {
	repo := newFakeRepo()
	repo.preferences = []domain.Preference{{
		AccountID: 42,
		Category:  domain.CategoryPromotion,
		Channel:   domain.ChannelInApp,
		IsEnabled: false,
	}}
	svc := newTestService(t, repo)

	_, err := svc.CreateNotification(t.Context(), accountapi.CreateNotificationRequest{
		AccountID: id.Of[id.Account](42),
		Category:  string(domain.CategoryPromotion),
		Title:     "50% off",
	})
	if err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}
	if len(repo.notifications) != 0 {
		t.Fatalf("wrote %d rows, want 0 — in-app is disabled", len(repo.notifications))
	}
}

func TestCreateNotificationRejectsUnknownCategory(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(t, repo)

	_, err := svc.CreateNotification(t.Context(), accountapi.CreateNotificationRequest{
		AccountID: id.Of[id.Account](42),
		Category:  "gossip",
		Title:     "t",
	})
	if !errors.Is(err, domain.ErrNotificationInvalid) {
		t.Fatalf("err = %v, want ErrNotificationInvalid", err)
	}
	if len(repo.notifications) != 0 {
		t.Errorf("wrote %d rows, want 0", len(repo.notifications))
	}
}
```

Reuse whatever `newFakeRepo` / `newTestService` helpers `internal/module/account/service_test.go` already defines; add a `preferences []domain.Preference` field to `fakeRepo` plus:

```go
func (f *fakeRepo) ListPreferences(_ context.Context, _ int64) ([]domain.Preference, error) {
	return f.preferences, nil
}
```

only if it is not already there (it is needed by `GetNotificationPreferences`, so it very likely is — check before adding).

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test -run '^TestCreateNotification' ./internal/module/account/`
Expected: FAIL — `svc.CreateNotification` undefined

- [ ] **Step 4: Implement the service method**

Append to `internal/module/account/service_notification.go`:

```go
// CreateNotification records one in-app notification, if the account wants it there.
//
// Only the in-app channel is written: it is the one this module owns a table for.
// Push, email and SMS are a workflow's problem, and a row that stood for "we tried
// to email you" would be a second, weaker definition of the feed.
func (s *Service) CreateNotification(ctx context.Context, req accountapi.CreateNotificationRequest) (accountapi.Notification, error) {
	accountID := req.AccountID.Int64()

	stored, err := s.repo.ListPreferences(ctx, accountID)
	if err != nil {
		return accountapi.Notification{}, fmt.Errorf("read notification preferences: %w", err)
	}
	category := domain.Category(req.Category)
	if !domain.Enabled(stored, category, domain.ChannelInApp) {
		return accountapi.Notification{}, nil
	}

	n, err := domain.NewNotification(domain.NewNotificationParams{
		AccountID: accountID,
		Category:  category,
		Title:     req.Title,
		Payload:   req.Payload,
	})
	if err != nil {
		return accountapi.Notification{}, err
	}

	id, err := s.repo.InsertNotification(ctx, n)
	if err != nil {
		return accountapi.Notification{}, fmt.Errorf("insert notification: %w", err)
	}
	n.ID = id
	return toAPINotification(n), nil
}
```

Add the resolver to `internal/module/account/domain/notification.go`. `Resolve` already folds stored rows over the defaults; `Enabled` is the single-pair question the service asks:

```go
// Enabled answers whether one category/channel pair is on, given the sparse stored
// rows: no row means the product default.
func Enabled(stored []Preference, c Category, ch Channel) bool {
	for _, p := range stored {
		if p.Category == c && p.Channel == ch {
			return p.IsEnabled
		}
	}
	return DefaultPreference(c, ch)
}
```

`toAPINotification` already exists in `service_notification.go` (used by `ListNotifications`); reuse it rather than writing a second mapper. If it is named differently, use whatever that file already calls.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -run '^TestCreateNotification' ./internal/module/account/`
Expected: PASS (3 tests)

- [ ] **Step 6: Write the subscriber test**

Create `internal/module/account/subscriber_test.go`:

```go
package account_test

import (
	"log/slog"
	"testing"

	"shopnexus/internal/infra/eventbus"
	"shopnexus/internal/module/account"
	"shopnexus/internal/module/order"
)

// The subscriber turns an order fact into one notification per interested party.
func TestSubscribeOrderEventsNotifiesBothSides(t *testing.T) {
	bus := eventbus.NewMemory()
	t.Cleanup(func() {
		if err := bus.Close(); err != nil {
			t.Errorf("close bus: %v", err)
		}
	})

	spy := &spyNotifier{done: make(chan struct{}, 4)}
	account.SubscribeOrderEvents(bus, spy, slog.New(slog.DiscardHandler))

	err := eventbus.Publish(t.Context(), bus, order.OrderPlacedTopic, order.OrderPlaced{
		OrderID:  9,
		BuyerID:  42,
		SellerID: 77,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	got := spy.wait(t, 2)
	if got[0].Category != "order" || got[1].Category != "order" {
		t.Errorf("categories = %q, %q, want order", got[0].Category, got[1].Category)
	}
	ids := map[int64]bool{got[0].AccountID.Int64(): true, got[1].AccountID.Int64(): true}
	if !ids[42] || !ids[77] {
		t.Errorf("recipients = %v, want buyer 42 and seller 77", ids)
	}
}
```

Add `spyNotifier` to the same file — it embeds the `accounttest` stub so an unstubbed method answers 501 rather than a plausible zero:

```go
type spyNotifier struct {
	accounttest.Service
	mu   sync.Mutex
	got  []accountapi.CreateNotificationRequest
	done chan struct{}
}

func (s *spyNotifier) CreateNotification(_ context.Context, req accountapi.CreateNotificationRequest) (accountapi.Notification, error) {
	s.mu.Lock()
	s.got = append(s.got, req)
	s.mu.Unlock()
	s.done <- struct{}{}
	return accountapi.Notification{}, nil
}

// wait blocks until n calls have landed, so the test never races the bus goroutine.
func (s *spyNotifier) wait(t *testing.T, n int) []accountapi.CreateNotificationRequest {
	t.Helper()
	for range n {
		select {
		case <-s.done:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for %d CreateNotification calls", n)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.got)
}
```

Check `internal/module/order/event.go` for the real field names on `OrderPlaced` before running — if it carries `BuyerID`/`SellerID` under other names, use those.

- [ ] **Step 7: Run the subscriber test to verify it fails**

Run: `go test -run '^TestSubscribeOrderEvents' ./internal/module/account/`
Expected: FAIL — `undefined: account.SubscribeOrderEvents`

- [ ] **Step 8: Implement the subscriber**

Create `internal/module/account/subscriber.go`:

```go
package account

import (
	"context"
	"fmt"
	"log/slog"

	"shopnexus/internal/infra/eventbus"
	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/module/order"
	"shopnexus/internal/shared/id"
)

// SubscribeOrderEvents turns order facts into feed rows.
//
// One notification per interested party, because a notification belongs to an
// account: an order fact has two of them and there is no shared inbox. The handler
// is not idempotent and does not need to be — a redelivered order event costs a
// duplicate feed row, which is a cosmetic fault, and de-duplicating it would need a
// uniqueness rule the feed does not have.
func SubscribeOrderEvents(bus eventbus.Client, svc accountapi.Service, log *slog.Logger) {
	eventbus.Subscribe(bus, order.OrderPlacedTopic, "account", func(ctx context.Context, e order.OrderPlaced) error {
		return notifyBoth(ctx, svc, log, e.BuyerID, e.SellerID, "Order placed", map[string]any{
			"order_id": id.Of[id.Order](e.OrderID),
		})
	})

	eventbus.Subscribe(bus, order.OrderSettledTopic, "account", func(ctx context.Context, e order.OrderSettled) error {
		return notifyBoth(ctx, svc, log, e.BuyerID, e.SellerID, "Order completed", map[string]any{
			"order_id": id.Of[id.Order](e.OrderID),
		})
	})
}

// notifyBoth writes to both sides and reports the first failure, so the bus retries
// the pair rather than silently delivering half of it.
func notifyBoth(ctx context.Context, svc accountapi.Service, log *slog.Logger, buyerID, sellerID int64, title string, payload map[string]any) error {
	for _, accountID := range [...]int64{buyerID, sellerID} {
		if accountID == 0 {
			continue
		}
		_, err := svc.CreateNotification(ctx, accountapi.CreateNotificationRequest{
			AccountID: id.Of[id.Account](accountID),
			Category:  string(domain.CategoryOrder),
			Title:     title,
			Payload:   payload,
		})
		if err != nil {
			log.Error("create order notification failed", "account_id", accountID, "title", title, "err", err)
			return fmt.Errorf("notify account %d: %w", accountID, err)
		}
	}
	return nil
}
```

- [ ] **Step 9: Run the subscriber test to verify it passes**

Run: `go test -run '^TestSubscribeOrderEvents' ./internal/module/account/`
Expected: PASS

- [ ] **Step 10: Register the subscriber with fx**

In `internal/module/account/fx.go`, add `fx.Invoke(SubscribeOrderEvents)` to the `fx.Module` invoke list, exactly as `internal/module/order/fx.go` registers `SubscribePaidSessions`.

**Watch the import direction:** `account` now imports `order`. Confirm `order` does not import `account` — run `go build ./...`; an import cycle fails the build. If there is a cycle, move the two topic declarations' payload structs into `orderapi` and subscribe on those instead.

- [ ] **Step 11: Full verification**

Run: `go build ./... && go vet ./... && go test ./... && golangci-lint run`
Expected: all clean, 0 lint issues

- [ ] **Step 12: Commit**

```bash
git add internal/module/account/
git commit -m "feat: notification producer for order events"
```

---

**Phase A is a shippable stop.** The feed now fills and `GET /notifications` returns rows. Everything below adds the push channel.

---

## Phase B1 — The Specification Pipeline

### Task 4: Extract `internal/shared/specmerge`

Pure refactor: no behaviour changes, and the existing `internal/shared/openapi/merge_test.go` is the proof.

**Files:**
- Create: `internal/shared/specmerge/specmerge.go`
- Modify: `internal/shared/openapi/merge.go`

**Interfaces:**
- Produces: `specmerge.Doc` (= `map[string]any`), `specmerge.FindRoot(dir string) (string, error)`, `specmerge.Read(path string) (Doc, error)`, `specmerge.Child(m Doc, key string) Doc`, `specmerge.MergeInto(dst, src Doc, file, kind string) error`, `specmerge.RenderYAML(d Doc) ([]byte, error)`

- [ ] **Step 1: Record the current behaviour**

Run: `go test ./internal/shared/openapi/`
Expected: PASS. This is the regression net for the whole task — if it is already failing, stop and fix that first.

- [ ] **Step 2: Create the new package**

Create `internal/shared/specmerge/specmerge.go`. This is `internal/shared/openapi/merge.go`'s helpers moved verbatim and exported — the only edits are the package clause, the names, and the doc comments:

```go
// Package specmerge holds the document-tree operations shared by the OpenAPI and
// AsyncAPI mergers.
//
// It is not a general YAML utility: MergeInto is where the duplicate-key invariant
// lives — two modules can never silently claim one name — and a second copy of that
// rule is a copy that can lose it while the first keeps it.
package specmerge

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Doc is a parsed specification document.
type Doc = map[string]any

// FindRoot walks up from dir to the module root (the directory holding go.mod).
func FindRoot(dir string) (string, error) {
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("specmerge: go.mod not found above " + dir)
		}
		dir = parent
	}
}

// Read parses one YAML document, answering an empty Doc for an empty file.
func Read(path string) (Doc, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read spec doc %s: %w", path, err)
	}
	var d Doc
	if err := yaml.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("specmerge: parse %s: %w", path, err)
	}
	if d == nil {
		d = Doc{}
	}
	return d, nil
}

// Child returns m[key] as a Doc, creating and storing it if absent.
func Child(m Doc, key string) Doc {
	if v, ok := m[key].(Doc); ok {
		return v
	}
	c := Doc{}
	m[key] = c
	return c
}

// MergeInto copies every entry of src into dst, failing on a duplicate key so two
// modules can never silently claim the same name.
func MergeInto(dst, src Doc, file, kind string) error {
	for k, v := range src {
		if _, exists := dst[k]; exists {
			return fmt.Errorf("specmerge: duplicate %s %q (also defined in %s)", kind, k, file)
		}
		dst[k] = v
	}
	return nil
}

// RenderYAML marshals a document to YAML.
func RenderYAML(d Doc) ([]byte, error) { return yaml.Marshal(d) }
```

- [ ] **Step 3: Rewrite `openapi/merge.go` to delegate**

Replace the whole file with the OpenAPI-shaped part only:

```go
// Package openapi merges the base document and per-aggregate OpenAPI fragments
// (internal/module/<module>/api/openapi/<aggregate>.yaml) into a single
// specification.
package openapi

import (
	"fmt"
	"path/filepath"
	"sort"

	"shopnexus/internal/shared/specmerge"
)

// FindRoot walks up from dir to the module root. Re-exported so callers that only
// speak OpenAPI need one import.
func FindRoot(dir string) (string, error) { return specmerge.FindRoot(dir) }

// RenderYAML marshals a document to YAML.
func RenderYAML(d specmerge.Doc) ([]byte, error) { return specmerge.RenderYAML(d) }

// MergeDoc reads api/openapi.base.yaml plus every
// internal/module/<module>/api/openapi/*.yaml under root and returns the merged
// specification as a document tree.
//
// Only paths and components.schemas are merged: anything reusable across modules —
// parameters, responses, security schemes — belongs in the base document.
func MergeDoc(root string) (specmerge.Doc, error) {
	base, err := specmerge.Read(filepath.Join(root, "api", "openapi.base.yaml"))
	if err != nil {
		return nil, err
	}
	paths := specmerge.Child(base, "paths")
	schemas := specmerge.Child(specmerge.Child(base, "components"), "schemas")

	frags, err := filepath.Glob(filepath.Join(root, "internal", "module", "*", "api", "openapi", "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("glob openapi fragments: %w", err)
	}
	sort.Strings(frags)
	for _, f := range frags {
		frag, err := specmerge.Read(f)
		if err != nil {
			return nil, err
		}
		if err := specmerge.MergeInto(paths, specmerge.Child(frag, "paths"), f, "path"); err != nil {
			return nil, err
		}
		fragSchemas := specmerge.Child(specmerge.Child(frag, "components"), "schemas")
		if err := specmerge.MergeInto(schemas, fragSchemas, f, "schema"); err != nil {
			return nil, err
		}
	}
	return base, nil
}

// Merge returns the merged spec as deterministic YAML bytes (served/embedded and
// published to the docs site).
func Merge(root string) ([]byte, error) {
	d, err := MergeDoc(root)
	if err != nil {
		return nil, err
	}
	return specmerge.RenderYAML(d)
}
```

`mergeDoc` becomes exported `MergeDoc` because Task 5's schema-closure copy needs the OpenAPI tree, not its bytes.

- [ ] **Step 4: Run the existing tests to verify the refactor is invisible**

Run: `go test ./internal/shared/openapi/ ./internal/shared/specmerge/`
Expected: PASS. `merge_test.go` may reference the old unexported `mergeDoc` — if it does, update the call to `MergeDoc` and nothing else.

- [ ] **Step 5: Confirm the generated document is byte-identical**

```bash
cp api/openapi.gen.yaml /tmp/openapi.before.yaml
go generate ./...
diff /tmp/openapi.before.yaml api/openapi.gen.yaml && echo "IDENTICAL"
```

Expected: `IDENTICAL`. A refactor that changes the served specification is not a refactor.

- [ ] **Step 6: Full verification**

Run: `go build ./... && go test ./... && golangci-lint run`
Expected: all clean

- [ ] **Step 7: Commit**

```bash
git add internal/shared/specmerge/ internal/shared/openapi/
git commit -m "refactor: extract specmerge from the openapi merger"
```

---

### Task 5: The AsyncAPI merger

**Files:**
- Create: `internal/shared/asyncapi/merge.go`
- Test: `internal/shared/asyncapi/merge_test.go`

**Interfaces:**
- Consumes: `specmerge.*` (Task 4), `openapi.MergeDoc` (Task 4)
- Produces: `asyncapi.Merge(root string) ([]byte, error)`, `asyncapi.MergeDoc(root string) (specmerge.Doc, error)`, `asyncapi.MessageCodes(d specmerge.Doc) []string`

- [ ] **Step 1: Write the failing test**

Create `internal/shared/asyncapi/merge_test.go`. The test builds a throwaway tree on disk so it does not depend on the real fragments moving under it:

```go
package asyncapi_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"shopnexus/internal/shared/asyncapi"
	"shopnexus/internal/shared/specmerge"
)

// writeTree lays out a minimal module root: go.mod, an OpenAPI base carrying the
// schema an event refers to, an AsyncAPI base, and one fragment per module.
func writeTree(t *testing.T, fragments map[string]string) string {
	t.Helper()
	root := t.TempDir()

	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	write("go.mod", "module shopnexus\n\ngo 1.26\n")
	write("api/openapi.base.yaml", `
openapi: 3.1.0
paths: {}
components:
  schemas:
    Message:
      type: object
      properties:
        id: { type: string }
        author: { $ref: '#/components/schemas/Account' }
    Account:
      type: object
      properties:
        id: { type: string }
    Unrelated:
      type: object
`)
	write("api/asyncapi.base.yaml", `
asyncapi: 3.0.0
info:
  title: Test
  version: 1.0.0
channels:
  userStream:
    address: /api/v1/ws
    messages: {}
operations:
  receiveUserEvents:
    action: receive
    channel:
      $ref: '#/channels/userStream'
`)
	for rel, body := range fragments {
		write(rel, body)
	}
	return root
}

const chatFragment = `
components:
  messages:
    MessageCreated:
      name: chat.message_created
      contentType: application/json
      payload:
        type: object
        required: [code, at, data]
        properties:
          code: { type: string, const: chat.message_created }
          at:   { type: string, format: date-time }
          data: { $ref: '#/components/schemas/Message' }
`

func TestMergeWiresMessagesIntoTheChannel(t *testing.T) {
	root := writeTree(t, map[string]string{
		"internal/module/chat/api/asyncapi/message.yaml": chatFragment,
	})

	doc, err := asyncapi.MergeDoc(root)
	if err != nil {
		t.Fatalf("MergeDoc: %v", err)
	}

	channel := specmerge.Child(specmerge.Child(doc, "channels"), "userStream")
	msgs := specmerge.Child(channel, "messages")
	ref, ok := msgs["MessageCreated"].(specmerge.Doc)
	if !ok {
		t.Fatalf("channel messages = %#v, want a MessageCreated $ref", msgs)
	}
	if got := ref["$ref"]; got != "#/components/messages/MessageCreated" {
		t.Errorf("$ref = %v", got)
	}
}

// An event payload may only point at a schema OpenAPI already publishes, and the
// generated document has to be self-contained, so the closure is copied in.
func TestMergeCopiesReferencedSchemaClosure(t *testing.T) {
	root := writeTree(t, map[string]string{
		"internal/module/chat/api/asyncapi/message.yaml": chatFragment,
	})

	doc, err := asyncapi.MergeDoc(root)
	if err != nil {
		t.Fatalf("MergeDoc: %v", err)
	}

	schemas := specmerge.Child(specmerge.Child(doc, "components"), "schemas")
	if _, ok := schemas["Message"]; !ok {
		t.Error("Message was not copied from the OpenAPI document")
	}
	// Account is reached only through Message.author — the copy must be transitive.
	if _, ok := schemas["Account"]; !ok {
		t.Error("Account was not copied; the closure is not transitive")
	}
	// Unrelated is referenced by nothing, so copying it would bloat every consumer.
	if _, ok := schemas["Unrelated"]; ok {
		t.Error("Unrelated was copied; only the referenced closure belongs here")
	}
}

func TestMergeRejectsAnUnknownSchemaRef(t *testing.T) {
	root := writeTree(t, map[string]string{
		"internal/module/chat/api/asyncapi/message.yaml": strings.ReplaceAll(
			chatFragment, "#/components/schemas/Message", "#/components/schemas/Nonexistent"),
	})

	_, err := asyncapi.MergeDoc(root)
	if err == nil {
		t.Fatal("MergeDoc succeeded; a ref to a schema OpenAPI does not publish must fail")
	}
	if !strings.Contains(err.Error(), "Nonexistent") {
		t.Errorf("err = %v, want it to name the missing schema", err)
	}
}

func TestMergeRejectsDuplicateMessageAcrossModules(t *testing.T) {
	root := writeTree(t, map[string]string{
		"internal/module/chat/api/asyncapi/message.yaml":  chatFragment,
		"internal/module/order/api/asyncapi/offer.yaml":    chatFragment,
	})

	_, err := asyncapi.MergeDoc(root)
	if err == nil {
		t.Fatal("MergeDoc succeeded; one flat namespace means a duplicate must fail")
	}
	if !strings.Contains(err.Error(), "MessageCreated") {
		t.Errorf("err = %v, want it to name the duplicate", err)
	}
}

func TestMessageCodes(t *testing.T) {
	root := writeTree(t, map[string]string{
		"internal/module/chat/api/asyncapi/message.yaml": chatFragment,
	})

	doc, err := asyncapi.MergeDoc(root)
	if err != nil {
		t.Fatalf("MergeDoc: %v", err)
	}
	got := asyncapi.MessageCodes(doc)
	if !slices.Equal(got, []string{"chat.message_created"}) {
		t.Errorf("MessageCodes = %v, want [chat.message_created]", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/shared/asyncapi/`
Expected: FAIL — the package does not exist

- [ ] **Step 3: Write the merger**

Create `internal/shared/asyncapi/merge.go`:

```go
// Package asyncapi merges the base document and per-aggregate AsyncAPI fragments
// (internal/module/<module>/api/asyncapi/<aggregate>.yaml) into a single
// specification describing the WebSocket surface.
//
// A fragment contributes only components.messages and components.schemas, into one
// flat namespace across every module. There is exactly one channel and it lives in
// the base document: this package wires a $ref to every merged message into it, so a
// module never has to name the channel.
package asyncapi

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"shopnexus/internal/shared/openapi"
	"shopnexus/internal/shared/specmerge"
)

// channelName is the single channel every event travels on. One socket per account
// carries every module's facts, so there is nothing to parameterise.
const channelName = "userStream"

const schemaRefPrefix = "#/components/schemas/"

// MergeDoc returns the merged AsyncAPI document as a tree.
func MergeDoc(root string) (specmerge.Doc, error) {
	base, err := specmerge.Read(filepath.Join(root, "api", "asyncapi.base.yaml"))
	if err != nil {
		return nil, err
	}
	components := specmerge.Child(base, "components")
	messages := specmerge.Child(components, "messages")
	schemas := specmerge.Child(components, "schemas")

	frags, err := filepath.Glob(filepath.Join(root, "internal", "module", "*", "api", "asyncapi", "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("glob asyncapi fragments: %w", err)
	}
	sort.Strings(frags)
	for _, f := range frags {
		frag, err := specmerge.Read(f)
		if err != nil {
			return nil, err
		}
		fragComponents := specmerge.Child(frag, "components")
		if err := specmerge.MergeInto(messages, specmerge.Child(fragComponents, "messages"), f, "message"); err != nil {
			return nil, err
		}
		if err := specmerge.MergeInto(schemas, specmerge.Child(fragComponents, "schemas"), f, "schema"); err != nil {
			return nil, err
		}
	}

	wireChannel(base, messages)

	if err := copySchemaClosure(root, base, schemas); err != nil {
		return nil, err
	}
	return base, nil
}

// Merge returns the merged document as deterministic YAML bytes.
func Merge(root string) ([]byte, error) {
	d, err := MergeDoc(root)
	if err != nil {
		return nil, err
	}
	return specmerge.RenderYAML(d)
}

// MessageCodes lists every message's name, sorted. It is what the contract test
// compares the Go event declarations against.
func MessageCodes(d specmerge.Doc) []string {
	messages := specmerge.Child(specmerge.Child(d, "components"), "messages")
	codes := make([]string, 0, len(messages))
	for _, v := range messages {
		msg, ok := v.(specmerge.Doc)
		if !ok {
			continue
		}
		if name, ok := msg["name"].(string); ok {
			codes = append(codes, name)
		}
	}
	sort.Strings(codes)
	return codes
}

// wireChannel points the single channel at every merged message.
func wireChannel(base, messages specmerge.Doc) {
	channel := specmerge.Child(specmerge.Child(base, "channels"), channelName)
	into := specmerge.Child(channel, "messages")
	for name := range messages {
		into[name] = specmerge.Doc{"$ref": "#/components/messages/" + name}
	}
}

// copySchemaClosure pulls every OpenAPI schema the document refers to, and
// everything those refer to in turn, into the AsyncAPI document.
//
// Two things fall out. The generated file is self-contained, so any tooling can read
// it. And an event can only carry a shape the REST API already publishes: a ref to
// something OpenAPI does not define fails here rather than shipping a second,
// divergent definition of the same entity.
func copySchemaClosure(root string, base, schemas specmerge.Doc) error {
	source, err := openapi.MergeDoc(root)
	if err != nil {
		return fmt.Errorf("merge openapi for schema closure: %w", err)
	}
	available := specmerge.Child(specmerge.Child(source, "components"), "schemas")

	// A schema already defined by a fragment wins: the closure only fills gaps.
	pending := refsIn(base)
	for len(pending) > 0 {
		name := pending[0]
		pending = pending[1:]
		if _, have := schemas[name]; have {
			continue
		}
		def, ok := available[name]
		if !ok {
			return fmt.Errorf("asyncapi: schema %q is not published by openapi", name)
		}
		schemas[name] = def
		pending = append(pending, refsIn(def)...)
	}
	return nil
}

// refsIn walks any decoded YAML value and collects the schema names it $refs.
func refsIn(v any) []string {
	var out []string
	switch t := v.(type) {
	case specmerge.Doc:
		for k, child := range t {
			if k == "$ref" {
				if s, ok := child.(string); ok {
					if name, found := strings.CutPrefix(s, schemaRefPrefix); found {
						out = append(out, name)
					}
				}
				continue
			}
			out = append(out, refsIn(child)...)
		}
	case []any:
		for _, child := range t {
			out = append(out, refsIn(child)...)
		}
	}
	return out
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/shared/asyncapi/`
Expected: PASS (5 tests)

`TestMergeRejectsDuplicateMessageAcrossModules` depends on map iteration order only in *which* duplicate is reported, not whether one is — if it flakes, the assertion on the name is wrong, not the merger.

- [ ] **Step 5: Lint**

Run: `go vet ./internal/shared/... && golangci-lint run ./internal/shared/...`
Expected: no output

- [ ] **Step 6: Commit**

```bash
git add internal/shared/asyncapi/
git commit -m "feat: asyncapi fragment merger"
```

---

### Task 6: Generate, embed and serve the document

**Files:**
- Create: `api/asyncapi.base.yaml`
- Create: `internal/module/chat/api/asyncapi/message.yaml`
- Modify: `cmd/specgen/main.go`
- Modify: `internal/shared/openapi/handler.go` (or wherever `SpecHandler` lives — find it with `grep -rn "func SpecHandler" internal/`)
- Modify: `internal/gateway/router.go:73-74`
- Create: `internal/gateway/asyncapi_contract_test.go`

**Interfaces:**
- Consumes: `asyncapi.Merge`, `asyncapi.MergeDoc`, `asyncapi.MessageCodes` (Task 5)
- Produces: `api/asyncapi.gen.yaml`; `GET /api/v1/asyncapi.yaml`

- [ ] **Step 1: Write the base document**

Create `api/asyncapi.base.yaml`. `servers[0].pathname` must agree with `api.BasePath` — a contract test asserts it, the same way `openapi.base.yaml`'s `servers[0].url` is guarded:

```yaml
asyncapi: 3.0.0

info:
  title: ShopNexus realtime
  version: 1.0.0
  description: |
    The push half of the ShopNexus API. One WebSocket per signed-in account carries
    every module's facts; the REST surface is described separately in openapi.yaml.

    The socket is receive-only. There is no client-to-server message: the client
    changes state through REST and learns about other people's changes here.

    Delivery is at-most-once and nothing is replayed. A client that reconnects has
    to assume it missed events, so it re-reads what it cares about over REST — which
    is why there is no cursor, no acknowledgement and no subscribe message.

servers:
  production:
    host: api.shopnexus.com
    protocol: wss
    pathname: /api/v1/ws
    description: Production. Obtain a ticket from POST /api/v1/ws/tickets first.

channels:
  userStream:
    address: /api/v1/ws
    title: Per-account event stream
    description: |
      Every fact the signed-in account is entitled to see. The account is established
      by the ticket at handshake time and is never named in a message: a socket only
      ever carries one account's events.
    bindings:
      ws:
        method: GET
        query:
          type: object
          required: [ticket]
          properties:
            ticket:
              type: string
              description: |
                Single-use ticket from POST /api/v1/ws/tickets, valid 30 seconds.
                Redeemed at handshake and immediately destroyed, so every reconnect
                needs a fresh one. The access token is never put in the URL.
              example: wst_9f3c1d7b4a2e8065f1a9c3d5e7b20418
    messages: {}

operations:
  receiveUserEvents:
    action: receive
    title: Receive account events
    channel:
      $ref: '#/channels/userStream'
```

`messages: {}` is filled by the merger. Leave it empty here.

- [ ] **Step 2: Write the chat fragment**

Create `internal/module/chat/api/asyncapi/message.yaml`. Chat's module-level prose belongs on its root aggregate fragment (`conversation.yaml` in the OpenAPI set), but chat has no AsyncAPI conversation fragment, so the prose sits here:

```yaml
# Chat's realtime surface. A message event goes to the conversation's *other*
# participant only: the sender already holds the row from the mutation response, and
# echoing it back would race their optimistic update.
components:
  messages:
    MessageCreated:
      name: chat.message_created
      title: Message created
      summary: Somebody sent a message in a conversation you are part of.
      contentType: application/json
      payload:
        type: object
        required: [code, at, data]
        properties:
          code:
            type: string
            const: chat.message_created
          at:
            type: string
            format: date-time
            description: When the backend published the event, not when the row was written.
            example: '2026-08-03T11:17:04Z'
          data:
            $ref: '#/components/schemas/Message'

    MessageUpdated:
      name: chat.message_updated
      title: Message edited
      summary: The other participant edited a message; body and edited_at changed.
      contentType: application/json
      payload:
        type: object
        required: [code, at, data]
        properties:
          code:
            type: string
            const: chat.message_updated
          at:
            type: string
            format: date-time
            example: '2026-08-03T11:17:04Z'
          data:
            $ref: '#/components/schemas/Message'

    MessageDeleted:
      name: chat.message_deleted
      title: Message deleted
      summary: The other participant deleted a message.
      contentType: application/json
      payload:
        type: object
        required: [code, at, data]
        properties:
          code:
            type: string
            const: chat.message_deleted
          at:
            type: string
            format: date-time
            example: '2026-08-03T11:17:04Z'
          data:
            $ref: '#/components/schemas/DeletedMessageRef'

    ConversationRead:
      name: chat.conversation_read
      title: Conversation read
      summary: The other participant read the thread up to an instant.
      contentType: application/json
      payload:
        type: object
        required: [code, at, data]
        properties:
          code:
            type: string
            const: chat.conversation_read
          at:
            type: string
            format: date-time
            example: '2026-08-03T11:17:04Z'
          data:
            $ref: '#/components/schemas/ConversationReadMark'
```

Two of those `data` shapes are not published by OpenAPI yet, so Task 5's closure check will refuse them. Add both to `internal/module/chat/api/openapi/message.yaml` under `components.schemas` — they are genuine parts of the contract, not AsyncAPI-only types:

```yaml
    DeletedMessageRef:
      type: object
      description: |
        Enough to find and drop a message from a rendered thread. Not the whole
        Message: a deleted row's body is gone, and sending an emptied entity would
        read as an edit.
      required: [id, conversation_id, created_at]
      properties:
        id:
          type: string
          description: Opaque message id.
          example: msg_2h9qk4mfx7bd3
        conversation_id:
          type: string
          example: cnv_7bd32h9qk4mfx
        created_at:
          type: string
          format: date-time
          description: The message's own instant — the hypertable needs it to locate the row.
          example: '2026-08-03T11:16:02Z'

    ConversationReadMark:
      type: object
      description: How far one participant has read a thread.
      required: [conversation_id, reader_id, read_at]
      properties:
        conversation_id:
          type: string
          example: cnv_7bd32h9qk4mfx
        reader_id:
          type: string
          description: Who read it — always the other participant, never the recipient.
          example: acc_4mfx7bd32h9qk
        read_at:
          type: string
          format: date-time
          example: '2026-08-03T11:17:04Z'
```

- [ ] **Step 3: Teach specgen to write both documents**

Replace `cmd/specgen/main.go`:

```go
// Command specgen merges the OpenAPI and AsyncAPI base documents and their
// per-module fragments into api/openapi.gen.yaml and api/asyncapi.gen.yaml (served,
// embedded, and published to docs). Run via `go generate ./...`.
package main

import (
	"log"
	"os"
	"path/filepath"

	"shopnexus/internal/shared/asyncapi"
	"shopnexus/internal/shared/openapi"
)

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	root, err := openapi.FindRoot(cwd)
	if err != nil {
		log.Fatal(err)
	}

	// OpenAPI first: the AsyncAPI merge reads it for the schema closure, so a broken
	// REST fragment should fail here rather than as a confusing missing-schema error.
	write(filepath.Join(root, "api", "openapi.gen.yaml"), func() ([]byte, error) {
		return openapi.Merge(root)
	})
	write(filepath.Join(root, "api", "asyncapi.gen.yaml"), func() ([]byte, error) {
		return asyncapi.Merge(root)
	})
}

func write(out string, merge func() ([]byte, error)) {
	merged, err := merge()
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(out, merged, 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("specgen: wrote %s (%d bytes)", out, len(merged))
}
```

- [ ] **Step 4: Generate and eyeball the result**

```bash
go generate ./...
head -40 api/asyncapi.gen.yaml
grep -c 'chat\.' api/asyncapi.gen.yaml
```

Expected: `asyncapi.gen.yaml` exists; the four `chat.*` codes appear; `components.schemas` contains `Message`, `DeletedMessageRef`, `ConversationReadMark` and whatever `Message` transitively refs (`ResourceDTO` at least).

If it fails with `schema "X" is not published by openapi`, the fragment refs a name the REST spec does not define — add it to the module's OpenAPI fragment as in Step 2.

- [ ] **Step 5: Embed and serve it**

Find the embed and handler: `grep -rn "openapi.gen.yaml" internal/`. Beside the existing `//go:embed` line add:

```go
//go:embed asyncapi.gen.yaml
var asyncAPISpec []byte
```

and beside `SpecHandler`:

```go
// AsyncAPISpecHandler serves the merged AsyncAPI document. Same reasoning as the
// OpenAPI one: the running server is the only authority on what it actually speaks.
func AsyncAPISpecHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	if _, err := w.Write(asyncAPISpec); err != nil {
		return
	}
}
```

Match the existing `SpecHandler`'s exact style — if it sets a different content type or handles errors differently, copy that.

In `internal/gateway/router.go`, beside line 73:

```go
	mux.HandleFunc("GET /asyncapi.yaml", openapi.AsyncAPISpecHandler)
```

- [ ] **Step 6: Write the contract test**

Create `internal/gateway/asyncapi_contract_test.go`. Model it on `openapi_contract_test.go` — reuse its helper for locating the root:

```go
package gateway_test

import (
	"slices"
	"testing"

	"shopnexus/internal/gateway/api"
	"shopnexus/internal/shared/asyncapi"
	"shopnexus/internal/shared/specmerge"
)

// wantCodes is every event the backend may publish. Adding an event means adding it
// here and in a fragment; the two lists disagreeing is the drift this test exists to
// catch. Keep it sorted.
var wantCodes = []string{
	"chat.conversation_read",
	"chat.message_created",
	"chat.message_deleted",
	"chat.message_updated",
}

func TestAsyncAPIMessageCodes(t *testing.T) {
	doc := mergedAsyncAPI(t)

	got := asyncapi.MessageCodes(doc)
	if !slices.Equal(got, wantCodes) {
		t.Errorf("message codes drifted\n got: %v\nwant: %v", got, wantCodes)
	}
}

// Every message must be reachable from the channel, or a client generated from the
// document never learns about it.
func TestAsyncAPIChannelReferencesEveryMessage(t *testing.T) {
	doc := mergedAsyncAPI(t)

	messages := specmerge.Child(specmerge.Child(doc, "components"), "messages")
	channel := specmerge.Child(specmerge.Child(doc, "channels"), "userStream")
	wired := specmerge.Child(channel, "messages")

	if len(wired) != len(messages) {
		t.Fatalf("channel wires %d messages, components define %d", len(wired), len(messages))
	}
	for name := range messages {
		ref, ok := wired[name].(specmerge.Doc)
		if !ok {
			t.Errorf("message %q is not wired into the channel", name)
			continue
		}
		if got := ref["$ref"]; got != "#/components/messages/"+name {
			t.Errorf("message %q wired as %v", name, got)
		}
	}
}

// The socket lives under the same base path as every route, and a client builds its
// URL from the document.
func TestAsyncAPIServerPathMatchesBasePath(t *testing.T) {
	doc := mergedAsyncAPI(t)

	servers := specmerge.Child(doc, "servers")
	production, ok := servers["production"].(specmerge.Doc)
	if !ok {
		t.Fatal("servers.production is missing")
	}
	want := api.BasePath + "/ws"
	if got := production["pathname"]; got != want {
		t.Errorf("pathname = %v, want %v", got, want)
	}
}

// Every payload is the same envelope, so a client can switch on one field.
func TestAsyncAPIPayloadsShareTheEnvelope(t *testing.T) {
	doc := mergedAsyncAPI(t)
	messages := specmerge.Child(specmerge.Child(doc, "components"), "messages")

	for name, v := range messages {
		msg, ok := v.(specmerge.Doc)
		if !ok {
			t.Errorf("message %q is not a mapping", name)
			continue
		}
		payload := specmerge.Child(msg, "payload")
		props := specmerge.Child(payload, "properties")
		for _, field := range []string{"code", "at", "data"} {
			if _, ok := props[field]; !ok {
				t.Errorf("message %q payload has no %q", name, field)
			}
		}
		code := specmerge.Child(props, "code")
		if code["const"] != msg["name"] {
			t.Errorf("message %q: payload code const = %v, message name = %v", name, code["const"], msg["name"])
		}
	}
}

func mergedAsyncAPI(t *testing.T) specmerge.Doc {
	t.Helper()
	root, err := specmerge.FindRoot(".")
	if err != nil {
		t.Fatalf("FindRoot: %v", err)
	}
	doc, err := asyncapi.MergeDoc(root)
	if err != nil {
		t.Fatalf("MergeDoc: %v", err)
	}
	return doc
}
```

If `api.BasePath` is not the right import path for the `/api/v1` constant, find it: `grep -rn "BasePath" internal/gateway/`.

- [ ] **Step 7: Run the contract tests**

Run: `go test -run '^TestAsyncAPI' ./internal/gateway/`
Expected: PASS (4 tests)

- [ ] **Step 8: Validate against a real AsyncAPI parser**

```bash
npx -y @asyncapi/cli@latest validate api/asyncapi.gen.yaml
```

Expected: `File api/asyncapi.gen.yaml is valid.` This is the check the Go test cannot make — our merger does not know the AsyncAPI meta-schema. Warnings about missing `contact`/`license` are fine; errors are not.

- [ ] **Step 9: Full verification**

Run: `go build ./... && go test ./... && golangci-lint run`
Expected: all clean

Also confirm the OpenAPI document is unchanged apart from the two new schemas:
```bash
git diff --stat api/openapi.gen.yaml
```
Expected: additions only.

- [ ] **Step 10: Commit**

```bash
git add api/ internal/module/chat/api/ cmd/specgen/ internal/shared/openapi/ internal/gateway/
git commit -m "feat: generate and serve the asyncapi document"
```

---

## Phase B2 — Transport Primitives

### Task 7: `cache.Client.GetDel`

**Files:**
- Modify: `internal/infra/cache/cache.go:14-22`
- Modify: `internal/infra/cache/redis.go`
- Modify: `internal/infra/cache/memory.go` (if one exists — `ls internal/infra/cache/`)
- Test: `internal/infra/cache/redis_integration_test.go`

**Interfaces:**
- Produces: `cache.Client.GetDel(ctx context.Context, key string, dest any) error` — returns `cache.ErrCacheMiss` when the key is absent, and the key is gone afterwards either way

- [ ] **Step 1: Write the failing integration test**

Create or append to `internal/infra/cache/redis_integration_test.go`. It needs `//go:build integration` at the top and must skip without Redis — copy the skip helper from whichever cache or eventbus integration test already exists (`internal/infra/eventbus/redis_test.go` has one):

```go
//go:build integration

package cache_test

import (
	"errors"
	"sync"
	"testing"

	"shopnexus/internal/infra/cache"
)

func TestGetDelReadsThenRemoves(t *testing.T) {
	c := newTestCache(t)
	key := "getdel-" + t.Name()

	if err := c.Set(t.Context(), key, "hello", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	var got string
	if err := c.GetDel(t.Context(), key, &got); err != nil {
		t.Fatalf("GetDel: %v", err)
	}
	if got != "hello" {
		t.Errorf("got %q, want hello", got)
	}

	var again string
	if err := c.GetDel(t.Context(), key, &again); !errors.Is(err, cache.ErrCacheMiss) {
		t.Fatalf("second GetDel err = %v, want ErrCacheMiss", err)
	}
}

func TestGetDelMissingKey(t *testing.T) {
	c := newTestCache(t)

	var got string
	err := c.GetDel(t.Context(), "getdel-absent-"+t.Name(), &got)
	if !errors.Is(err, cache.ErrCacheMiss) {
		t.Fatalf("err = %v, want ErrCacheMiss", err)
	}
}

// This is the property the whole ticket scheme rests on: concurrent redemptions of
// one key must produce exactly one winner.
func TestGetDelIsAtomic(t *testing.T) {
	c := newTestCache(t)
	key := "getdel-race-" + t.Name()

	if err := c.Set(t.Context(), key, "once", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	const racers = 16
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		wins  int
		other []error
	)
	for range racers {
		wg.Go(func() {
			var got string
			err := c.GetDel(t.Context(), key, &got)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				wins++
			case errors.Is(err, cache.ErrCacheMiss):
			default:
				other = append(other, err)
			}
		})
	}
	wg.Wait()

	if len(other) > 0 {
		t.Fatalf("unexpected errors: %v", other)
	}
	if wins != 1 {
		t.Fatalf("%d racers read the value, want exactly 1", wins)
	}
}
```

If `newTestCache` does not exist, add it:

```go
// newTestCache connects to REDIS_ADDR, skipping when it is unset — the same contract
// every integration test here follows.
func newTestCache(t *testing.T) cache.Client {
	t.Helper()
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		t.Skip("REDIS_ADDR not set")
	}
	rdb, err := rueidis.NewClient(rueidis.ClientOption{InitAddress: []string{addr}})
	if err != nil {
		t.Fatalf("connect redis: %v", err)
	}
	c := cache.NewRedisClient(rdb)
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Errorf("close cache: %v", err)
		}
	})
	return c
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -tags integration -run '^TestGetDel' ./internal/infra/cache/`
Expected: FAIL — `c.GetDel` undefined

- [ ] **Step 3: Add the method to the interface**

In `internal/infra/cache/cache.go`, after `Get`:

```go
	// GetDel reads a key and removes it in one command, so a value that must be
	// consumed exactly once cannot be read twice. Get-then-Delete is two commands and
	// two concurrent callers both win.
	GetDel(ctx context.Context, key string, dest any) error
```

- [ ] **Step 4: Implement it on the Redis client**

In `internal/infra/cache/redis.go`, after `Get` — the body mirrors `Get` exactly, so a reader sees the one difference:

```go
func (r *RedisClient) GetDel(ctx context.Context, key string, dest any) error {
	resp := r.Client.Do(ctx, r.Client.B().Getdel().Key(key).Build())
	if err := resp.Error(); err != nil {
		if errors.Is(err, rueidis.Nil) {
			return ErrCacheMiss
		}
		return fmt.Errorf("getdel redis key %s: %w", key, err)
	}

	str, err := resp.ToString()
	if err != nil {
		return fmt.Errorf("read redis value for %s: %w", key, err)
	}

	if err = json.Unmarshal([]byte(str), dest); err != nil {
		return fmt.Errorf("decode cache value for %s: %w", key, err)
	}

	return nil
}
```

- [ ] **Step 5: Implement it on every other `Client`**

Run `grep -rln "cache.Client = " internal/` and `grep -rn "func.*Exists(ctx context.Context, key string)" internal/` to find every implementation (an in-memory one for tests, possibly a fake in a module's tests). Each needs a `GetDel`. For a mutex-guarded map implementation:

```go
func (m *Memory) GetDel(ctx context.Context, key string, dest any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// One critical section covering both halves — the point of the method.
	if err := m.getLocked(key, dest); err != nil {
		return err
	}
	delete(m.values, key)
	return nil
}
```

Adjust to the real field and helper names in that file. `go build ./...` names every implementation you missed.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test -tags integration -run '^TestGetDel' ./internal/infra/cache/`
Expected: PASS (3 tests, including the 16-way race)

- [ ] **Step 7: Full verification**

Run: `go build ./... && go test ./... && golangci-lint run`
Expected: all clean

- [ ] **Step 8: Commit**

```bash
git add internal/infra/cache/
git commit -m "feat: atomic getdel on the cache client"
```

---

### Task 8: The handshake ticket

**Files:**
- Create: `internal/shared/session/ticket.go`
- Test: `internal/shared/session/ticket_integration_test.go`

**Interfaces:**
- Consumes: `cache.Client.GetDel` (Task 7), `session.Store.Lookup` (`session.go:95`)
- Produces: `session.NewTickets(c cache.Client, ttl time.Duration) *Tickets`; `(*Tickets).Issue(ctx context.Context, accountID int64, sessionID string) (string, error)`; `(*Tickets).Redeem(ctx context.Context, ticket string) (accountID int64, sessionID string, err error)`; `session.ErrInvalidTicket`

- [ ] **Step 1: Write the failing test**

Create `internal/shared/session/ticket_integration_test.go`:

```go
//go:build integration

package session_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"shopnexus/internal/shared/session"
)

func TestTicketRoundTrip(t *testing.T) {
	tickets := session.NewTickets(newTestCache(t), 30*time.Second)

	tok, err := tickets.Issue(t.Context(), 42, "sess-abc")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if len(tok) < 20 {
		t.Fatalf("ticket %q is too short to be unguessable", tok)
	}

	accountID, sessionID, err := tickets.Redeem(t.Context(), tok)
	if err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	if accountID != 42 {
		t.Errorf("accountID = %d, want 42", accountID)
	}
	if sessionID != "sess-abc" {
		t.Errorf("sessionID = %q, want sess-abc", sessionID)
	}
}

// The property that makes a ticket safe in a URL: seeing it in a log is worthless
// once the real client has connected.
func TestTicketIsSingleUse(t *testing.T) {
	tickets := session.NewTickets(newTestCache(t), 30*time.Second)

	tok, err := tickets.Issue(t.Context(), 42, "sess-abc")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, _, err := tickets.Redeem(t.Context(), tok); err != nil {
		t.Fatalf("first Redeem: %v", err)
	}

	_, _, err = tickets.Redeem(t.Context(), tok)
	if !errors.Is(err, session.ErrInvalidTicket) {
		t.Fatalf("second Redeem err = %v, want ErrInvalidTicket", err)
	}
}

func TestTicketConcurrentRedeemHasOneWinner(t *testing.T) {
	tickets := session.NewTickets(newTestCache(t), 30*time.Second)

	tok, err := tickets.Issue(t.Context(), 42, "sess-abc")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		wins int
	)
	for range 16 {
		wg.Go(func() {
			if _, _, err := tickets.Redeem(t.Context(), tok); err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	if wins != 1 {
		t.Fatalf("%d redemptions succeeded, want 1", wins)
	}
}

func TestRedeemUnknownTicket(t *testing.T) {
	tickets := session.NewTickets(newTestCache(t), 30*time.Second)

	_, _, err := tickets.Redeem(t.Context(), "wst_deadbeef")
	if !errors.Is(err, session.ErrInvalidTicket) {
		t.Fatalf("err = %v, want ErrInvalidTicket", err)
	}
}

func TestRedeemEmptyTicket(t *testing.T) {
	tickets := session.NewTickets(newTestCache(t), 30*time.Second)

	_, _, err := tickets.Redeem(t.Context(), "")
	if !errors.Is(err, session.ErrInvalidTicket) {
		t.Fatalf("err = %v, want ErrInvalidTicket", err)
	}
}

func TestTicketExpires(t *testing.T) {
	tickets := session.NewTickets(newTestCache(t), time.Second)

	tok, err := tickets.Issue(t.Context(), 42, "sess-abc")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	time.Sleep(1500 * time.Millisecond)

	_, _, err = tickets.Redeem(t.Context(), tok)
	if !errors.Is(err, session.ErrInvalidTicket) {
		t.Fatalf("err = %v, want ErrInvalidTicket after the TTL", err)
	}
}
```

`newTestCache` here is the session package's own helper — if `internal/shared/session` has no integration test yet, copy the one from Task 7 Step 1 into this file.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -tags integration -run '^TestTicket|^TestRedeem' ./internal/shared/session/`
Expected: FAIL — `undefined: session.NewTickets`

- [ ] **Step 3: Implement the store**

Create `internal/shared/session/ticket.go`:

```go
package session

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"shopnexus/internal/infra/cache"
	"shopnexus/internal/shared/errx"
)

// ErrInvalidTicket covers a ticket that never existed, already ran, or expired.
// Indistinguishable on purpose: the three are the same fact to a client, and telling
// them apart would confirm that a ticket seen in a log was once real.
var ErrInvalidTicket = errx.NewError(http.StatusUnauthorized, "invalid_ticket", "ticket is not valid")

const ticketKeyPrefix = "ws-ticket:"

// ticketPrefix marks a ticket in a log or a URL as what it is. It is not an opaque
// id, so it does not go through shared/id's cipher: an id is a permanent name and
// this is a secret that dies in thirty seconds.
const ticketPrefix = "wst_"

// Tickets hands out single-use handshake credentials for the WebSocket.
//
// A browser cannot set Authorization on new WebSocket(), and putting the access token
// in the query string writes a live fifteen-minute credential into every proxy log,
// Loki and the user's history. A ticket is the same trade every one-time secret in
// this codebase makes: a Redis key with a TTL, read exactly once and then gone.
type Tickets struct {
	cache cache.Client
	ttl   time.Duration
}

func NewTickets(c cache.Client, ttl time.Duration) *Tickets {
	return &Tickets{cache: c, ttl: ttl}
}

// ticket is what a ticket key holds: the session as well as the account, because the
// handshake has to re-check that the session is still alive.
type ticket struct {
	AccountID int64  `json:"account_id"`
	SessionID string `json:"session_id"`
}

// Issue mints a ticket for a caller the normal Bearer middleware has already
// authenticated.
func (t *Tickets) Issue(ctx context.Context, accountID int64, sessionID string) (string, error) {
	tok, err := randomToken()
	if err != nil {
		return "", err
	}
	tok = ticketPrefix + tok
	rec := ticket{AccountID: accountID, SessionID: sessionID}
	if err := t.cache.Set(ctx, ticketKeyPrefix+tok, rec, t.ttl); err != nil {
		return "", fmt.Errorf("store ws ticket: %w", err)
	}
	return tok, nil
}

// Redeem consumes a ticket and answers who it belongs to.
//
// The caller must still check the session: a ticket issued a moment before a logout
// is a valid ticket for a dead session, and letting it open a socket rebuilds exactly
// the hole middleware.Auth pays a Redis lookup per request to close.
func (t *Tickets) Redeem(ctx context.Context, tok string) (int64, string, error) {
	if tok == "" {
		return 0, "", ErrInvalidTicket
	}
	var rec ticket
	if err := t.cache.GetDel(ctx, ticketKeyPrefix+tok, &rec); err != nil {
		if errors.Is(err, cache.ErrCacheMiss) {
			return 0, "", ErrInvalidTicket
		}
		return 0, "", fmt.Errorf("read ws ticket: %w", err)
	}
	if rec.AccountID == 0 || rec.SessionID == "" {
		return 0, "", ErrInvalidTicket
	}
	return rec.AccountID, rec.SessionID, nil
}
```

`randomToken` is the package's existing 32-hex-char generator (`session.go:216`) — reuse it rather than adding a second random-handle format.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -tags integration -run '^TestTicket|^TestRedeem' ./internal/shared/session/`
Expected: PASS (6 tests)

- [ ] **Step 5: Full verification**

Run: `go build ./... && go test ./... && golangci-lint run`
Expected: all clean

- [ ] **Step 6: Commit**

```bash
git add internal/shared/session/
git commit -m "feat: single-use websocket handshake tickets"
```

---

### Task 9: Core NATS fan-out

**Files:**
- Create: `internal/infra/eventbus/fanout.go`
- Test: `internal/infra/eventbus/fanout_test.go`

**Interfaces:**
- Consumes: `*eventbus.NATS` (`nats.go:39`), its `conn *nats.Conn` field
- Produces: `(*NATS).Broadcast(subject string, payload []byte) error`; `(*NATS).OnBroadcast(subject string, h func([]byte)) (cancel func(), err error)`

- [ ] **Step 1: Write the failing test**

Create `internal/infra/eventbus/fanout_test.go`. It needs `//go:build integration` and the existing skip helper from `nats_test.go`:

```go
//go:build integration

package eventbus_test

import (
	"testing"
	"time"

	"shopnexus/internal/infra/eventbus"
)

// The defining property: two subscribers to one subject both receive every message.
// JetStream's durable consumers would have split them.
func TestBroadcastReachesEverySubscriber(t *testing.T) {
	bus := newTestNATS(t)
	subject := uniqueName("ws.test.fanout.")

	first := make(chan []byte, 4)
	second := make(chan []byte, 4)

	cancelFirst, err := bus.OnBroadcast(subject, func(b []byte) { first <- b })
	if err != nil {
		t.Fatalf("OnBroadcast: %v", err)
	}
	defer cancelFirst()

	cancelSecond, err := bus.OnBroadcast(subject, func(b []byte) { second <- b })
	if err != nil {
		t.Fatalf("OnBroadcast: %v", err)
	}
	defer cancelSecond()

	if err := bus.Broadcast(subject, []byte(`{"code":"x"}`)); err != nil {
		t.Fatalf("Broadcast: %v", err)
	}

	for i, ch := range []chan []byte{first, second} {
		select {
		case got := <-ch:
			if string(got) != `{"code":"x"}` {
				t.Errorf("subscriber %d got %q", i, got)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("subscriber %d received nothing", i)
		}
	}
}

// Cancelling must actually unsubscribe, or a replica that dropped its last socket
// keeps paying for the traffic.
func TestOnBroadcastCancelStopsDelivery(t *testing.T) {
	bus := newTestNATS(t)
	subject := uniqueName("ws.test.cancel.")

	got := make(chan []byte, 4)
	cancel, err := bus.OnBroadcast(subject, func(b []byte) { got <- b })
	if err != nil {
		t.Fatalf("OnBroadcast: %v", err)
	}

	if err := bus.Broadcast(subject, []byte("first")); err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive the first message")
	}

	cancel()

	if err := bus.Broadcast(subject, []byte("second")); err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	select {
	case b := <-got:
		t.Fatalf("received %q after cancel", b)
	case <-time.After(300 * time.Millisecond):
	}
}

// Nothing persists: a message published with no listener is gone, which is why a
// reconnecting client re-reads over REST instead of expecting a replay.
func TestBroadcastWithNoSubscriberIsDropped(t *testing.T) {
	bus := newTestNATS(t)
	subject := uniqueName("ws.test.nolistener.")

	if err := bus.Broadcast(subject, []byte("into the void")); err != nil {
		t.Fatalf("Broadcast: %v", err)
	}

	got := make(chan []byte, 1)
	cancel, err := bus.OnBroadcast(subject, func(b []byte) { got <- b })
	if err != nil {
		t.Fatalf("OnBroadcast: %v", err)
	}
	defer cancel()

	select {
	case b := <-got:
		t.Fatalf("received %q; core pub/sub must not replay", b)
	case <-time.After(300 * time.Millisecond):
	}
}

// Subject filtering happens on the server, so a replica holding no socket for an
// account pays nothing for its events.
func TestBroadcastIsFilteredBySubject(t *testing.T) {
	bus := newTestNATS(t)
	mine, theirs := uniqueName("ws.test.mine."), uniqueName("ws.test.theirs.")

	got := make(chan []byte, 4)
	cancel, err := bus.OnBroadcast(mine, func(b []byte) { got <- b })
	if err != nil {
		t.Fatalf("OnBroadcast: %v", err)
	}
	defer cancel()

	if err := bus.Broadcast(theirs, []byte("not for me")); err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	select {
	case b := <-got:
		t.Fatalf("received %q from another subject", b)
	case <-time.After(300 * time.Millisecond):
	}
}
```

`newTestNATS` and `uniqueName` already exist in `internal/infra/eventbus/nats_test.go` — reuse them. If `newTestNATS` returns `eventbus.Client`, add a variant returning `*eventbus.NATS`, since `Broadcast` is not on the interface.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -tags integration -run '^TestBroadcast|^TestOnBroadcast' ./internal/infra/eventbus/`
Expected: FAIL — `bus.Broadcast` undefined

- [ ] **Step 3: Implement fan-out**

Create `internal/infra/eventbus/fanout.go`:

```go
package eventbus

import (
	"fmt"

	"github.com/nats-io/nats.go"
)

// fanoutSubjectPrefix keeps ephemeral fan-out out of the JetStream stream's subject
// space (bus.>), so a broadcast is never captured by a durable consumer.
const fanoutSubjectPrefix = "fanout."

// Broadcast publishes to every current subscriber of subject and to nobody else.
//
// Core NATS, deliberately not JetStream. JetStream's Subscribe creates a durable pull
// consumer, so replicas sharing one would split the messages between them instead of
// each receiving all — and a durable per replica leaves a consumer behind for every
// process that ever ran, orphaned outright by a kill -9. Nothing here is persisted:
// a message with no listener is dropped, which is the right semantics for a live
// socket, because a client that was disconnected re-reads over REST rather than
// replaying a day of history into a fresh connection.
func (n *NATS) Broadcast(subject string, payload []byte) error {
	if err := n.conn.Publish(fanoutSubjectPrefix+subject, payload); err != nil {
		return fmt.Errorf("eventbus: broadcast %s: %w", subject, err)
	}
	return nil
}

// OnBroadcast delivers every message on subject to h until cancel is called.
//
// h runs on the connection's dispatch goroutine, so it must not block: a handler that
// waits stalls delivery for every subject this connection carries, not just this one.
func (n *NATS) OnBroadcast(subject string, h func([]byte)) (func(), error) {
	sub, err := n.conn.Subscribe(fanoutSubjectPrefix+subject, func(msg *nats.Msg) {
		h(msg.Data)
	})
	if err != nil {
		return nil, fmt.Errorf("eventbus: subscribe broadcast %s: %w", subject, err)
	}
	return func() {
		// The caller is going away regardless, so a failed unsubscribe is worth a log
		// at most — and the connection closing will drop it anyway.
		if err := sub.Unsubscribe(); err != nil {
			n.logger.Warn("eventbus: unsubscribe broadcast failed", "subject", subject, "err", err)
		}
	}, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -tags integration -run '^TestBroadcast|^TestOnBroadcast' ./internal/infra/eventbus/`
Expected: PASS (4 tests)

Bring NATS up first if needed: `docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d` and export `NATS_URL`.

- [ ] **Step 5: Full verification**

Run: `go build ./... && go test ./... && golangci-lint run`
Expected: all clean

- [ ] **Step 6: Commit**

```bash
git add internal/infra/eventbus/
git commit -m "feat: core nats fan-out for realtime delivery"
```

---

### Task 10: The `realtime` envelope

**Files:**
- Create: `internal/shared/realtime/realtime.go`
- Test: `internal/shared/realtime/realtime_test.go`

**Interfaces:**
- Consumes: nothing (defines its own `Fanout`, satisfied structurally by `*eventbus.NATS` from Task 9)
- Produces: `realtime.Fanout`; `realtime.Event[T]`; `realtime.NewEvent[T](code string) Event[T]`; `realtime.Notify[T](ctx context.Context, f Fanout, accountID int64, e Event[T], data T) error`; `realtime.AccountSubject(accountID int64) string`; `realtime.Envelope`

- [ ] **Step 1: Write the failing test**

Create `internal/shared/realtime/realtime_test.go`:

```go
package realtime_test

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"shopnexus/internal/shared/realtime"
)

type payload struct {
	ID   string `json:"id"`
	Body string `json:"body"`
}

var thingHappened = realtime.NewEvent[payload]("test.thing_happened")

// fakeFanout records what was published, so a service test never needs a bus.
type fakeFanout struct {
	mu   sync.Mutex
	sent []sentMessage
	err  error
}

type sentMessage struct {
	subject string
	payload []byte
}

func (f *fakeFanout) Broadcast(subject string, b []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, sentMessage{subject: subject, payload: b})
	return nil
}

func (f *fakeFanout) OnBroadcast(string, func([]byte)) (func(), error) {
	return func() {}, nil
}

func TestNotifyBuildsTheEnvelope(t *testing.T) {
	f := &fakeFanout{}
	before := time.Now()

	err := realtime.Notify(t.Context(), f, 42, thingHappened, payload{ID: "x", Body: "hi"})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}

	if len(f.sent) != 1 {
		t.Fatalf("published %d messages, want 1", len(f.sent))
	}
	if got, want := f.sent[0].subject, realtime.AccountSubject(42); got != want {
		t.Errorf("subject = %q, want %q", got, want)
	}

	var env struct {
		Code string    `json:"code"`
		At   time.Time `json:"at"`
		Data payload   `json:"data"`
	}
	if err := json.Unmarshal(f.sent[0].payload, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Code != "test.thing_happened" {
		t.Errorf("code = %q", env.Code)
	}
	if env.At.Before(before) {
		t.Errorf("at = %v, want at or after %v", env.At, before)
	}
	if env.Data.Body != "hi" {
		t.Errorf("data.body = %q, want hi", env.Data.Body)
	}
}

// One socket carries exactly one account's events, so the subject is the whole
// authorisation and it must not collide across accounts.
func TestAccountSubjectIsPerAccount(t *testing.T) {
	if realtime.AccountSubject(1) == realtime.AccountSubject(2) {
		t.Fatal("two accounts share a subject")
	}
	if got := realtime.AccountSubject(42); got != "ws.acct.42" {
		t.Errorf("AccountSubject(42) = %q, want ws.acct.42", got)
	}
}

func TestNotifyRejectsAnUnaddressedEvent(t *testing.T) {
	f := &fakeFanout{}

	err := realtime.Notify(t.Context(), f, 0, thingHappened, payload{ID: "x"})
	if err == nil {
		t.Fatal("Notify succeeded with accountID 0; there is no such recipient")
	}
	if len(f.sent) != 0 {
		t.Errorf("published %d messages, want 0", len(f.sent))
	}
}

func TestNotifyWrapsATransportFailure(t *testing.T) {
	sentinel := errors.New("bus down")
	f := &fakeFanout{err: sentinel}

	err := realtime.Notify(t.Context(), f, 42, thingHappened, payload{ID: "x"})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want it to wrap %v", err, sentinel)
	}
}

func TestDecodeEnvelope(t *testing.T) {
	f := &fakeFanout{}
	if err := realtime.Notify(t.Context(), f, 42, thingHappened, payload{ID: "x", Body: "hi"}); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	env, err := realtime.DecodeEnvelope(f.sent[0].payload)
	if err != nil {
		t.Fatalf("DecodeEnvelope: %v", err)
	}
	if env.Code != "test.thing_happened" {
		t.Errorf("code = %q", env.Code)
	}

	got, ok := realtime.DataOf(env, thingHappened)
	if !ok {
		t.Fatal("DataOf reported the wrong event")
	}
	if got.Body != "hi" {
		t.Errorf("body = %q, want hi", got.Body)
	}

	other := realtime.NewEvent[payload]("test.something_else")
	if _, ok := realtime.DataOf(env, other); ok {
		t.Error("DataOf matched a different event's code")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/shared/realtime/`
Expected: FAIL — the package does not exist

- [ ] **Step 3: Write the package**

Create `internal/shared/realtime/realtime.go`:

```go
// Package realtime is the one place that knows what a pushed event looks like on the
// wire and which subject carries it.
//
// A service records a fact by calling Notify; the gateway's hub decodes with
// DecodeEnvelope. Both sides of the socket therefore agree by construction, and the
// AsyncAPI document describes exactly one shape.
package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// subjectPrefix namespaces per-account fan-out subjects.
const subjectPrefix = "ws.acct."

// ErrNoRecipient is a programming error surfacing as a value: an event with no
// account to deliver it to has no subject, and silently dropping it would hide the
// bug until somebody noticed a missing badge.
var ErrNoRecipient = errors.New("realtime: event has no recipient account")

// Fanout is the transport an event crosses to reach every gateway replica. Declared
// here rather than imported so shared/ still depends on nothing in infra/;
// *eventbus.NATS satisfies it structurally.
type Fanout interface {
	Broadcast(subject string, payload []byte) error
	OnBroadcast(subject string, h func([]byte)) (cancel func(), err error)
}

// Event binds a code to the payload published with it — the same pairing
// eventbus.Topic[T] makes for the durable bus. Nothing else names the code string, so
// a typo cannot compile and then match nothing.
type Event[T any] struct{ Code string }

func NewEvent[T any](code string) Event[T] { return Event[T]{Code: code} }

// Envelope is what travels: a code a client switches on, the instant the backend
// published it, and the payload. Data stays deferred so the hub can forward bytes it
// never needs to understand.
type Envelope struct {
	Code string          `json:"code"`
	At   time.Time       `json:"at"`
	Data json.RawMessage `json:"data"`
}

// AccountSubject is the subject carrying one account's events. Per-account rather than
// one shared subject filtered in process, so NATS does the filtering and a replica
// holding no socket for that account receives no bytes at all.
func AccountSubject(accountID int64) string {
	return subjectPrefix + strconv.FormatInt(accountID, 10)
}

// Notify pushes one fact to every live socket of accountID.
//
// Callers treat this as best-effort: the row is already committed, so an unreachable
// bus is a stale interface, never a failed request. It returns an error anyway,
// because whether to log or to retry is the caller's call, not this package's.
//
// One account per call. A fact with two interested parties is two calls, because the
// caller is the only side that knows the relationship — and the subject is the whole
// authorisation, so addressing is not something to guess at here.
func Notify[T any](ctx context.Context, f Fanout, accountID int64, e Event[T], data T) error {
	if accountID == 0 {
		return fmt.Errorf("%w: code %s", ErrNoRecipient, e.Code)
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode %s payload: %w", e.Code, err)
	}
	body, err := json.Marshal(Envelope{Code: e.Code, At: time.Now().UTC(), Data: raw})
	if err != nil {
		return fmt.Errorf("encode %s envelope: %w", e.Code, err)
	}
	if err := f.Broadcast(AccountSubject(accountID), body); err != nil {
		return fmt.Errorf("broadcast %s: %w", e.Code, err)
	}
	_ = ctx // the transport is fire-and-forget; the parameter keeps call sites uniform
	return nil
}

// DecodeEnvelope parses a fan-out message.
func DecodeEnvelope(b []byte) (Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(b, &env); err != nil {
		return Envelope{}, fmt.Errorf("decode realtime envelope: %w", err)
	}
	if env.Code == "" {
		return Envelope{}, errors.New("realtime: envelope has no code")
	}
	return env, nil
}

// DataOf reads an envelope's payload as e's type, answering false for a different
// event — so nobody decodes the wrong shape out of a raw message.
func DataOf[T any](env Envelope, e Event[T]) (T, bool) {
	var zero T
	if env.Code != e.Code {
		return zero, false
	}
	var out T
	if err := json.Unmarshal(env.Data, &out); err != nil {
		return zero, false
	}
	return out, true
}
```

The `_ = ctx` line will trip `unused-parameter` style checks in some configurations. If `golangci-lint` complains, drop the parameter — but keep it if it passes: every other publish path in this codebase takes a context, and a caller who has to remember which one does not is a caller who will pass the wrong thing later.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/shared/realtime/`
Expected: PASS (5 tests)

- [ ] **Step 5: Confirm `*eventbus.NATS` satisfies `Fanout`**

Add to `internal/infra/eventbus/fanout.go` — a compile-time assertion beats discovering it at wiring time:

```go
// Compile-time proof that *NATS is what realtime.Fanout describes. eventbus cannot
// import realtime (that would invert shared→infra), so the assertion lives here as a
// structural check against a hand-copied interface.
var _ interface {
	Broadcast(subject string, payload []byte) error
	OnBroadcast(subject string, h func([]byte)) (func(), error)
} = (*NATS)(nil)
```

- [ ] **Step 6: Full verification**

Run: `go build ./... && go test ./... && golangci-lint run`
Expected: all clean

- [ ] **Step 7: Commit**

```bash
git add internal/shared/realtime/ internal/infra/eventbus/fanout.go
git commit -m "feat: realtime event envelope and subject scheme"
```

---

I'll continue with Tasks 11 through 18 in the next pass.
