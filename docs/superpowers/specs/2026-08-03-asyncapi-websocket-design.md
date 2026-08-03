# AsyncAPI + WebSocket realtime

Date: 2026-08-03
Status: approved, not yet implemented

## Why

OpenAPI 3.x cannot describe a WebSocket. It is built around HTTP
request/response — `paths` → method → `responses` — while a socket is
bidirectional and message-driven, and `ws://` is not a valid `servers[0].url`.
So the realtime surface either goes undocumented or gets its own specification.

AsyncAPI 3.0 is that specification: same JSON Schema vocabulary as OpenAPI, so
the fragments read like the ones already in this repository, and it has native
WebSocket bindings.

Two things in the product are polling today because there was no push channel:
`useChatUnreadCount` and the notification `useUnreadCount` both run
`refetchInterval: 60_000`. A message from the other side of a negotiation takes
up to a minute to appear, and the two halves of a chat thread never agree on
what has been read.

## Scope

Two phases, sequenced. Phase B reads what phase A writes; the reverse is not
true, so A ships first and is useful over REST alone even if B slips.

**Phase A — the notification producer.** `port.Repository` has
`ListNotifications`, `CountUnreadNotifications` and `MarkNotificationsRead` and
no insert: the feed is readable and nothing writes it. Phase A adds the write
path, `in-app` channel only.

**Phase B — the WebSocket transport and its AsyncAPI specification.**

Explicitly out of scope: `push`, `email` and `sms` notification channels
(`domain/notification.go:18` already records those as "a Restate workflow's
problem", and that stays true); client→server messages of any kind.

## Decisions

| Decision | Choice | Why not the alternative |
|---|---|---|
| WS library | `github.com/coder/websocket` | Go 1.26 has no WebSocket in the standard library — verified: no `net/http/websocket`, nothing matching `websock` under `$GOROOT/src/net/http/`. Hand-rolling RFC 6455 over `http.ResponseController.Hijack()` is ~400–500 lines of frame codec, masking, fragmentation and close handshake against browsers we do not control. `coder/websocket` is zero-dependency, context-aware and autobahn-tested. |
| Fan-out | core NATS pub/sub | JetStream would not have solved it: `NATS.Subscribe` (`internal/infra/eventbus/nats.go:186`) creates a *durable pull consumer* named `group_topic`, so replicas sharing a durable load-balance the messages exactly as a Redis consumer group does. Per-instance durables would work but leave a consumer behind for every replica that ever ran, and `kill -9` orphans them with `MaxAckPending` 10k accumulating. Core pub/sub is true fan-out with no durable state to reap. |
| Delivery guarantee | none, at-most-once | A dropped event is repaired by invalidating on reconnect (see below), which is cheaper and more correct than replaying a persisted stream into a socket whose owner may have been offline for a day. |
| Handshake auth | single-use Redis ticket | A browser cannot set headers on `new WebSocket()`, and `middleware.Auth` (`internal/gateway/middleware/auth.go:27`) reads only `Authorization: Bearer`. `?token=<jwt>` puts a live 15-minute credential into proxy access logs, Loki and browser history. The ticket matches a convention this codebase already states: "One-time secrets live in Redis, not in a table — each is read once and then has to disappear, which is a TTL". |
| Client→server | nothing, v1 | `c.CloseRead(ctx)` means no state machine, no inbound validation and no `action: send` in the specification. A typing indicator later is one message plus one operation, breaking nothing. |

## Architecture

```
service (chat / order / account)
   │  realtime.Notify(ctx, fanout, accountID, MessageCreated, payload)
   ▼
core NATS pub/sub    subject: ws.acct.<accountID>       (no JetStream)
   │
   ├──► gateway replica #1 ──► Hub ──► that account's sockets on this replica
   ├──► gateway replica #2 ──► Hub ──► (none held here → nothing delivered)
   └──► gateway replica #3 ──► Hub ──► …
```

Fan-out is per-account subject rather than one shared subject filtered in
process, so NATS does the filtering server-side and a replica holding no socket
for that account receives no bytes. The Hub subscribes a subject when an
account's first socket arrives and cancels it when the last one leaves.

`order.placed` and `order.settled` already publish to the Redis bus and their
producers do not change. A bridge subscriber in the gateway reads them from
Redis and calls `realtime.Notify`. Its consumer group is a single shared
`ws-bridge`: whichever replica receives it re-publishes to the NATS fan-out, and
every replica gets it from there.

## The specification pipeline

Mirrors the OpenAPI pipeline exactly.

| OpenAPI (existing) | AsyncAPI (new) |
|---|---|
| `api/openapi.base.yaml` | `api/asyncapi.base.yaml` |
| `internal/module/*/api/openapi/*.yaml` | `internal/module/*/api/asyncapi/*.yaml` |
| → `api/openapi.gen.yaml` | → `api/asyncapi.gen.yaml` |
| `cmd/specgen` | same `cmd/specgen`, writes both |
| `internal/gateway/openapi_contract_test.go` | `internal/gateway/asyncapi_contract_test.go` |

A fragment contributes only `components.messages` and `components.schemas`,
into one flat namespace across every module, and a duplicate key fails the
merge — the same rule the OpenAPI merge enforces. There is exactly one channel
and it lives in the base document; the merger fills
`channels.userStream.messages` with a `$ref` per merged message, so a module
never names the channel.

`internal/shared/openapi/merge.go` holds four helpers that are pure YAML-tree
operations and that the AsyncAPI merge needs identically: `readDoc`, `child`,
`mergeInto`, `RenderYAML` (plus `FindRoot`). They move to
`internal/shared/specmerge`, leaving `openapi` and `asyncapi` holding only
their own document shapes. The name says the purpose rather than the mechanism
because `MergeInto` is not a convenience — it is where the duplicate-key
invariant lives, and a second copy of it is a copy that can lose the invariant
while the first keeps it.

### Base document

```yaml
asyncapi: 3.0.0
info:
  title: ShopNexus realtime
  version: 1.0.0
servers:
  production:
    host: api.shopnexus.com
    protocol: wss
    pathname: /api/v1/ws
channels:
  userStream:
    address: /api/v1/ws
    title: Per-account event stream
    bindings:
      ws:
        method: GET
        query:
          type: object
          required: [ticket]
          properties:
            ticket:
              type: string
              description: Single-use ticket from POST /ws/tickets, TTL 30s.
              example: wst_3f9qk4mfx7bd3
    messages: {}
operations:
  receiveUserEvents:
    action: receive
    channel:
      $ref: '#/channels/userStream'
```

The ticket is described by `bindings.ws.query`, which is what WebSocket
bindings exist for, rather than by prose.

### Fragment shape

`internal/module/chat/api/asyncapi/message.yaml`:

```yaml
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
```

An event payload's `data` may only `$ref` a schema the OpenAPI document already
publishes. After merging both documents `cmd/specgen` copies the transitive
closure of the schemas AsyncAPI references out of the merged OpenAPI document
into `asyncapi.gen.yaml`. Three things follow: the generated document is
self-contained and therefore valid for any tooling including AsyncAPI Studio;
there is never a second definition of `Message`, so a socket cannot carry a
shape REST does not know; and the website gets its TypeScript for free, because
every `data` type already exists in the hey-api output.

Per-module prose sits at the top of the module's root aggregate fragment, the
same convention the OpenAPI fragments follow.

### Event catalogue, v1

| Code | Module | What it replaces |
|---|---|---|
| `chat.message_created` | chat *(new topic)* | `useChatUnreadCount` 60s poll; inbox self-invalidation |
| `chat.message_updated` | chat *(new)* | an edit only the sender could see |
| `chat.message_deleted` | chat *(new)* | as above |
| `chat.conversation_read` | chat *(new)* | read receipts, which do not exist today |
| `order.offer_updated` | order *(new)* | a negotiation both sides watch live |
| `order.placed` | order *(exists)* | the order page after the payment webhook lands |
| `order.settled` | order *(exists)* | escrow closing |
| `account.notification_created` | account *(phase A)* | notification `useUnreadCount` 60s poll |

## Backend

```
api/asyncapi.base.yaml                       + channel, server, ws bindings
internal/shared/specmerge/                   + extracted from openapi/merge.go
internal/shared/asyncapi/merge.go            + AsyncAPI doc shape + schema closure copy
internal/shared/realtime/realtime.go         + Fanout, Event[T], Notify, subject scheme
internal/shared/session/ticket.go            + Tickets: Issue / Redeem
internal/infra/eventbus/fanout.go            + Broadcast/OnBroadcast on *NATS (core, no JS)
internal/infra/cache/cache.go                ~ + GetDel
internal/gateway/ws/hub.go                   + Hub, client, write pump
internal/gateway/handler/ws.go               + GET /ws, POST /ws/tickets
internal/gateway/router.go                   ~ mount both; /ws outside the auth chain
internal/gateway/asyncapi_contract_test.go   + validity + Go↔spec drift guard
internal/module/chat/event.go                + four Event[T]
internal/module/chat/service.go              ~ Notify after each write
internal/module/order/event.go               ~ + OfferUpdated
internal/config/                             ~ six required env vars
cmd/specgen/main.go                          ~ writes both documents
```

### The realtime seam

`realtime` declares the interface it needs; `*eventbus.NATS` satisfies it
structurally, so `shared` still imports nothing from `infra`.

```go
package realtime

type Fanout interface {
	Broadcast(subject string, payload []byte) error
	OnBroadcast(subject string, h func([]byte)) (cancel func(), err error)
}

// Event binds a code to the payload published with it — the same pairing
// eventbus.Topic[T] makes for the durable bus, so a typo'd literal cannot
// compile and silently match nothing.
type Event[T any] struct{ Code string }

func NewEvent[T any](code string) Event[T] { return Event[T]{Code: code} }

// Notify pushes one fact to every live socket of accountID. Best-effort: the row
// is already committed, so an unreachable bus is a stale UI, never a failed
// request.
func Notify[T any](ctx context.Context, f Fanout, accountID int64, e Event[T], data T) error
```

Events are declared in `<module>/event.go`, beside the existing
`eventbus.Topic` declarations (`internal/module/order/event.go:26`):

```go
var MessageCreated = realtime.NewEvent[chatapi.Message]("chat.message_created")
```

`realtime` is the only package that knows the subject scheme
(`ws.acct.<accountID>`) and the only one that builds the `{code, at, data}`
envelope, so the shape on the wire has exactly one author.

### Who receives an event

`Notify` addresses one account, so a fact with two interested parties is two
calls. The caller decides, because the caller is the only side that knows the
relationship — there is no fan-out-to-a-group primitive and no group membership
to keep correct:

| Event | Recipients |
|---|---|
| `chat.message_created` / `_updated` / `_deleted` | the conversation's other participant, not the sender — the sender already has the row from the mutation response, and echoing it back would race the optimistic update |
| `chat.conversation_read` | the other participant, for whom "they read it" is news |
| `order.offer_updated` | both buyer and seller: either side may hold the standing proposal |
| `order.placed` / `order.settled` | both buyer and seller |
| `account.notification_created` | the account the row belongs to |

A caller that gets this wrong leaks: `Notify` does not check that the recipient
is entitled to the payload, because the subject *is* the authorisation. So the
`data` on an event must be a shape both recipients may already read over REST —
which is enforced by the rule that `data` only `$ref`s a published OpenAPI
schema, but not by the type system. Sending a seller-only projection to a buyer
is a review question, and worth naming in the handler.

### The ticket

`cache.Client` (`internal/infra/cache/cache.go:14`) has `Get`, `Set`, `Delete`
and `Exists` and no `GETDEL`. Redeeming with `Get` then `Delete` is not atomic:
two concurrent redemptions of one ticket both win and single-use is gone —
which is the property that limits the damage when a ticket does reach an access
log. So `cache.Client` grows `GetDel(ctx context.Context, key string, dest any) error`
(rueidis supports `GETDEL`, Redis 6.2+). Same guarded-write reasoning as
everywhere else in this codebase: a stale read has to lose.

A ticket is `wst_` plus 32 bytes of `crypto/rand` in Crockford base32. It is a
secret, not an identifier, so it does not go through `shared/id`'s Feistel
codec.

Redeeming is two checks, not one:

```go
accountID, sessionID, err := tickets.Redeem(ctx, r.URL.Query().Get("ticket"))
if err != nil { … }
if _, err := sessions.Lookup(ctx, sessionID); err != nil { … }
```

The second is required: a ticket issued before a logout must not open a socket.
Skipping it rebuilds exactly the hole `middleware.Auth` pays one Redis lookup
per request to close.

### The Hub

```go
type Hub struct {
	mu   sync.RWMutex
	subs map[int64]*accountSub // accountID → sockets + the OnBroadcast cancel
}
```

- An account's first socket subscribes its subject; the last one to leave
  cancels it. No subject stays subscribed with no owner.
- Each client owns a buffered `out chan []byte`. **A full buffer closes the
  socket and never blocks.** Core NATS delivers on the connection's dispatch
  goroutine, so blocking there stalls fan-out for the whole process rather than
  for one slow client.
- One write pump per client with a write deadline; keepalive via `c.Ping(ctx)`
  on an interval.
- A cap on sockets per account, so one tab-spammer cannot exhaust the
  descriptors.

### Observability

`/ws` is excluded from `Sink.Middleware`, and connection count becomes a
`runtime_metrics` gauge instead. Left in, a socket held for thirty minutes
enters `http_requests_1m` as a thirty-minute request and
`approx_percentile(0.95, "latency")` stops meaning anything.

### Configuration

Six new variables, all required with no defaults, per
`internal/config`'s existing rule: ticket TTL, write timeout, ping interval,
send buffer size, max sockets per account, allowed origins (for
`AcceptOptions.OriginPatterns`). Added to `internal/config`,
`docker-compose.yml` and `README.md`.

### Phase A in detail

```
port.Repository          + InsertNotification(ctx, domain.Notification) error
adapter/postgres         + the insert
account/subscriber.go    + Redis subscriber: order.placed/settled → notification row
domain/notification.go   ~ + NewNotification(...) with validation
```

`Preference` decides whether an `in-app` row is written at all. The other three
channels are untouched.

## Website

```
scripts/gen-ws-events.mjs          + generates TS from asyncapi.yaml
src/api/generated/ws-events.ts      + generated, gitignored like its siblings
src/realtime/client.ts              + connect, ticket, backoff reconnect
src/realtime/handlers.ts            + event → cache operation
src/realtime/RealtimeProvider.tsx   + one connection per app
src/hooks/api/useChat.ts            ~ drop refetchInterval
src/hooks/api/useNotifications.ts   ~ drop refetchInterval
package.json                        ~ + "gen:ws", devDependency "yaml"
```

The union is generated, not hand-written. `npm run gen:ws` reads
`../server/api/asyncapi.gen.yaml` — the sibling checkout, by path, with no copy
in the website repository.

That is deliberately *not* what OpenAPI does today, and the difference is a bug
worth fixing in the same pass. `openapi-ts.config.ts:3-6` states that reading
from the sibling rather than a copy is deliberate, "because the copy that used
to live at website/openapi.yaml had drifted 18 paths behind" — but
`input: "./openapi.yaml"` is that copy, it is tracked in git, and it has drifted
again: 421,944 bytes against the generated document's 423,749. So the frontend
is once more generating a client from a specification the server does not serve.
Point `input` at `../server/api/openapi.gen.yaml`, delete
`website/openapi.yaml`, and the comment becomes true.

```ts
export type RealtimeEvent =
	| { code: "chat.message_created"; at: string; data: Message }
	| { code: "chat.message_updated"; at: string; data: Message }
	| { code: "order.offer_updated"; at: string; data: Offer }
	| { code: "account.notification_created"; at: string; data: Notification }
	// …

export type RealtimeCode = RealtimeEvent["code"]
```

Being generated there is no drift to guard, so the website needs no contract
test for it. A discriminated union over a string literal `code` is also what
`.antigravity/typescript-conventions/SKILL.md` asks for.

Every reconnect requests a fresh ticket, because a ticket is single-use.
Backoff is jittered and capped at 30s, and an `online` event reconnects
immediately rather than waiting the backoff out.

**Every successful connect invalidates the queries the socket feeds.**

```ts
onOpen: () => invalidate(queryClient, OPERATIONS.notificationsUnread, OPERATIONS.notifications, …)
```

A disconnect is when events are lost — core NATS fan-out does not persist, by
the decision above. Invalidating on connect closes precisely that gap, and it
is what makes removing the polls safe rather than merely cheaper: without it a
silently dead socket freezes the badge indefinitely.

Cache policy — invalidate is the default and only chat takes the fast path:

| Event | Handling |
|---|---|
| `chat.message_created` | `setQueryData` prepending to page 0 of MESSAGES when that thread is open; otherwise invalidate CONVERSATIONS and UNREAD |
| everything else | `invalidate` through the existing `OPERATIONS` |

Chat earns it on frequency: `useMessages` pages 50 rows, so invalidating per
message refetches 50 rows to show one. Prepend rather than append because the
cursor walks newest-first and `useChat.ts:76` reverses for rendering. Every
other event is low-frequency, where invalidate is both sufficient and unable to
desynchronise a cache.

`NEXT_PUBLIC_WS_URL` is its own variable, not `http`→`ws` string surgery on the
API base: real deployments split the host, and a missing variable that fails
loudly beats a wrong host debugged through CORS errors.

## Testing

- Hub unit tests against a fake `Fanout`, no NATS: fan-out to multiple sockets,
  drop-on-full-buffer, cleanup on disconnect, subject cancelled when the last
  socket leaves.
- `Tickets` integration test (`//go:build integration`, skipped without Redis),
  covering redeem-twice and redeem-after-logout.
- `asyncapi_contract_test.go`: the merged document is valid, and every event
  code declared in Go has a matching message in it.
- Website: `npm run typecheck` covers the generated union; the handler table is
  exercised through the existing Playwright setup.

## Notes

`CLAUDE.md:183` links `docs/superpowers/specs/2026-07-28-proxy-id-design.md`,
which does not exist in this repository — there was no `docs/` directory before
this document. Worth either restoring the file or dropping the link.
