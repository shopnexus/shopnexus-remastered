# Rybbit — product / web analytics (self-hosted)

Rybbit handles **product & web analytics** (traffic, funnels, retention, session
replay, Core Web Vitals) for the marketplace **frontend**. It is a **separate
self-hosted stack** — it runs its own ClickHouse, Postgres, backend, client, and
Caddy — and is deliberately **not** wired into this repo's `docker-compose.yml`.
The Go backend does not depend on it; instrumentation is client-side.

Split of responsibilities:
- **Rybbit** → frontend user behavior (anonymous/product analytics). ClickHouse.
- **`observability` module** (this repo) → backend operational telemetry (HTTP
  RED, Go runtime, bus events) in TimescaleDB + Grafana.
- **Recommendation/personalization** (per-user, if/when built) → first-party
  interaction data server-side in TimescaleDB, joinable with catalog — not Rybbit.

## Deploy (latest)

Run on its own VPS (min 2 GB RAM), with a domain pointed at it (DNS A record).
The setup script generates env/secrets, builds the containers, and configures
Caddy for automatic TLS.

```bash
git clone https://github.com/rybbit-io/rybbit.git   # latest
cd rybbit
chmod +x *.sh
./setup.sh your.analytics.domain                    # e.g. analytics.shopnexus.com
```

For local runs or putting it behind an existing reverse proxy, follow Rybbit's
"Manual Docker Compose Setup" / "Advanced Self-Hosting" docs:
https://rybbit.com/docs/self-hosting

## Instrument the frontend

After setup, open the Rybbit dashboard, create a **Site**, and copy the tracking
snippet it shows (a `<script>` with your `site-id`) into the marketplace
frontend's `<head>` — shape:

```html
<script defer src="https://your.analytics.domain/api/script.js" data-site-id="YOUR_SITE_ID"></script>
```

Use the exact `src` and `data-site-id` from the dashboard. The snippet lives in
the **frontend** app (not this backend repo).

## Optional: server-side events

For events the browser can't see (e.g. `order.placed`), you can POST to Rybbit's
track API from the backend. Prefer keeping such domain signals **first-party in
TimescaleDB** if they feed recommendations; only forward to Rybbit for
product-analytics dashboards.

> Do not vendor Rybbit's source into this repo — deploy it from its own
> repository so it upgrades independently.
