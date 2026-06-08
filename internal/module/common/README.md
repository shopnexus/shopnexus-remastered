# Common Module

Shared infrastructure services: resource management, object storage, service options registry, geocoding, and server-sent events.

## ER Diagram

<!--START_SECTION:mermaid-->
```mermaid
erDiagram
"common.resource_reference" }o--|| "common.resource" : "rs_id"
"common.option" }o--|o "common.resource" : "logo_rs_id"

"common.option" {
  varchar(100) id
  uuid owner_id
  boolean is_enabled
  text name
  text description
  integer priority
  uuid logo_rs_id
  jsonb data
  text type
  text provider
}
"common.resource" {
  uuid id
  uuid uploaded_by_id
  text provider
  varchar(2048) object_key
  varchar(100) mime
  bigint size
  jsonb metadata
  text checksum
  timestamptz created_at
}
"common.resource_reference" {
  bigint id
  uuid rs_id
  resource_ref_type ref_type
  uuid ref_id
  integer order
}
```
<!--END_SECTION:mermaid-->

## Domain Concepts

### Resource Management

Polymorphic file attachments for any entity via a `resource_reference` join table. The `ref_type` enum discriminator (`ProductSpu`, `ProductSku`, `Refund`, `ReturnDispute`, `Comment`) links resources to their owning entity without foreign keys. Each resource tracks its storage provider, object key, MIME type, size, and checksum.

Resources are ordered per entity — the `order` column on `resource_reference` controls display order (e.g., first image is the product thumbnail).

### Object Storage

Three backends, each registered as a service option at startup:

| Provider | Description |
|----------|-------------|
| `local` | Local filesystem (`./tmp/uploads`) |
| `s3` | AWS S3 or MinIO with optional CloudFront CDN |
| `remote` | Passthrough for externally-hosted URLs |

Each resource record tracks its `provider`, so URLs resolve against the correct backend. Falls back to a configurable placeholder image on resolution failure.

### Service Options Registry

A generic registry for configurable providers (payment, transport, objectstore). Each option has an ID, category, provider, name, description, and JSONB config. Other modules call `UpdateServiceOptions` to register their providers on startup — the registry auto-syncs.

### Geocoding

Reverse/forward geocoding and location search via a pluggable provider interface. Currently uses Nominatim (OpenStreetMap). Used by the account module for contact address resolution.

### Server-Sent Events (SSE)

Real-time event stream for push notifications, chat messages, and other live updates. Authenticates via `Authorization` header or `?token=` query param.

## Implementation Notes

- **Transactional replace-all**: `UpdateResources` deletes existing refs, verifies new resource IDs exist, and re-creates refs in order — all within a single transaction. This simplifies the frontend: just send the full list of resource IDs in the desired order.
- **Auto-sync on startup**: each module registers its providers by calling `UpdateServiceOptions` during fx initialization. The registry upserts by ID, so restarts don't create duplicates.
- **Placeholder fallback**: if a resource URL can't be resolved (deleted file, broken S3 link), the system returns a configurable placeholder image URL instead of failing.

## Endpoints

All under `/api/v1/common`.

| Method | Path | Description |
|--------|------|-------------|
| POST | `/files` | Upload file via multipart/form-data, returns resource with URL |
| GET | `/option` | List active service options by `category` query param |
| POST | `/geocode/reverse` | Convert lat/lng to address |
| POST | `/geocode/forward` | Convert address to lat/lng |
| GET | `/geocode/search` | Location suggestions for partial query (`q`, `limit` params) |
| GET | `/stream` | SSE stream for real-time events |

## Exchange Rates

The `exchange.Client` (CurrencyAPI-backed) is wrapped in `exchange.CachingClient`,
a read-through cache keyed on `(base, sorted targets)`. `GetExchangeRates` calls
`FetchLatest` directly: lookups hit the upstream provider only on the first call
after the cache TTL (`exchange.refreshInterval`, default 6h) expires. All errors
fail open — an empty `Rates` map is returned and callers display original currency.

Use the `ConvertAmount` biz method for backend-side cross-currency math.
Frontend reads the snapshot via `GET /api/v1/common/currencies/rates`.

Config keys (under `app.exchange`):

- `base` — storage base currency (USD)
- `supported` — whitelist of supported codes
- `refreshInterval` — cron interval
- `httpTimeout` — upstream HTTP timeout
- `defaultUserCurrency` — new-profile default
- `upstreamURL` — Frankfurter endpoint
