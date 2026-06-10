package common

import (
	"net/http"
	"time"

	"go.uber.org/fx"

	"shopnexus-server/internal/infras/cache"
	"shopnexus-server/internal/infras/fxinfra"
	commonbiz "shopnexus-server/internal/module/common/biz"
	commonconfig "shopnexus-server/internal/module/common/config"
	commondb "shopnexus-server/internal/module/common/db/sqlc"
	commonecho "shopnexus-server/internal/module/common/transport/echo"
	"shopnexus-server/internal/provider/exchange"
	"shopnexus-server/internal/provider/geocoding"
	"shopnexus-server/internal/shared/pgsqlc"
)

// forwardGeocodeCacheTTL is long because address -> country mappings rarely
// change; the cache is the primary defence against Nominatim rate limits.
const forwardGeocodeCacheTTL = 30 * 24 * time.Hour

// Module provides the common module dependencies. The pool/cache/logger
// providers are fx.Private — each is constructed from THIS module's own
// Postgres/Redis/Log config and is invisible to other modules' fx graphs,
// so 8 modules can each `Provide(... pgsqlc.TxBeginner ...)` without
// colliding.
var Module = fx.Module("common",
	fxinfra.Providers[*commonconfig.Config]("common"),
	fx.Provide(
		commonconfig.NewConfig,
		NewCommonStorage,
		NewExchangeClient,
		NewGeocodingClient,
		commonbiz.NewcommonBiz,
		NewCommonBiz,
		commonecho.NewHandler,
	),
	fx.Invoke(
		commonecho.NewHandler,
	),
)

// NewCommonStorage creates a new common storage backed by PostgreSQL.
func NewCommonStorage(pool pgsqlc.TxBeginner) commonbiz.CommonStorage {
	return pgsqlc.NewStorage(pool, commondb.New(pool))
}

// NewCommonBiz creates the common client. BestEffort calls run in-process.
func NewCommonBiz(cfg *commonconfig.Config, biz *commonbiz.CommonHandler) commonbiz.CommonBizClient {
	return commonbiz.NewCommonBizClientInProcess(cfg.Restate.IngressAddress, biz)
}

// NewExchangeClient provides a CurrencyAPI-backed exchange.Client wrapped in a
// read-through cache. CurrencyAPI chosen over Frankfurter for full ISO 4217
// coverage (VND, COP, CLP etc. that ECB-based providers don't ship). The cache
// TTL is the refresh window; rates are re-fetched lazily on the first lookup
// after expiry.
func NewExchangeClient(cfg *commonconfig.Config, cache cache.Client) exchange.Client {
	client := exchange.NewCurrencyAPI(
		cfg.Exchange.UpstreamURL,
		cfg.Exchange.APIKey,
		&http.Client{Timeout: cfg.Exchange.HTTPTimeout},
	)

	ttl := cfg.Exchange.RefreshInterval
	if ttl <= 0 {
		ttl = 6 * time.Hour
	}
	return exchange.NewCachingClient(client, cache, ttl)
}

// NewGeocodingClient provides a Nominatim-backed geocoding.Client wrapped in a
// read-through cache on forward lookups. The 30-day TTL bounds staleness while
// keeping forward geocoding within Nominatim's 1 req/sec public rate limit.
func NewGeocodingClient(cache cache.Client) geocoding.Client {
	client := geocoding.NewNominatimProvider()
	return geocoding.NewCachingClient(client, cache, forwardGeocodeCacheTTL)
}
