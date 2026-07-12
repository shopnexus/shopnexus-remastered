package common

import (
	"net/http"
	"time"

	restate "github.com/restatedev/sdk-go"
	"go.uber.org/fx"

	"shopnexus-server/config"
	"shopnexus-server/internal/infras/cache"
	"shopnexus-server/internal/infras/infra"
	commonbiz "shopnexus-server/internal/module/common/biz"
	commondb "shopnexus-server/internal/module/common/db/sqlc"
	commonecho "shopnexus-server/internal/module/common/transport/echo"
	"shopnexus-server/internal/provider/exchange"
	"shopnexus-server/internal/provider/geocoding"
	"shopnexus-server/internal/shared/besteffort"
	"shopnexus-server/internal/shared/pgsqlc"
	"shopnexus-server/internal/shared/restatesvc"
)

// forwardGeocodeCacheTTL is long because address -> country mappings rarely
// change; the cache is the primary defence against Nominatim rate limits.
const forwardGeocodeCacheTTL = 30 * 24 * time.Hour

// Module provides the common module. Infra is its own fx.Private set via
// infra.StandardModule, built from the shared config.
var Module = fx.Module("common",
	infra.StandardModule("common"),
	fx.Provide(
		NewCommonStorage,
		NewExchangeClient,
		NewGeocodingClient,
		commonbiz.NewcommonBiz,
		NewCommonBiz,
		commonecho.NewHandler,
	),
	fx.Provide(
		fx.Annotate(
			func(b *commonbiz.CommonHandler) restate.ServiceDefinition {
				return restatesvc.Reflect(commonbiz.NewCommonService(b))
			},
			fx.ResultTags(`group:"restate"`),
		),
		fx.Annotate(
			func(b *commonbiz.CommonHandler) besteffort.Registrar {
				return func(s *besteffort.Server) { commonbiz.RegisterCommonBestEffort(s, b) }
			},
			fx.ResultTags(`group:"besteffort"`),
		),
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
func NewCommonBiz(cfg *config.Config, biz *commonbiz.CommonHandler) commonbiz.CommonBizClient {
	return commonbiz.NewCommonBizClientInProcess(cfg.Restate.IngressAddress, biz)
}

// NewExchangeClient provides a CurrencyAPI-backed exchange.Client wrapped in a
// read-through cache. CurrencyAPI chosen over Frankfurter for full ISO 4217
// coverage (VND, COP, CLP etc. that ECB-based providers don't ship). The cache
// TTL is the refresh window; rates are re-fetched lazily on the first lookup
// after expiry.
func NewExchangeClient(cfg *config.Config, cache cache.Client) exchange.Client {
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
