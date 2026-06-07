package common

import (
	"net/http"

	"go.uber.org/fx"

	"shopnexus-server/internal/infras/fxinfra"
	commonbiz "shopnexus-server/internal/module/common/biz"
	commonconfig "shopnexus-server/internal/module/common/config"
	commondb "shopnexus-server/internal/module/common/db/sqlc"
	commonecho "shopnexus-server/internal/module/common/transport/echo"
	"shopnexus-server/internal/provider/exchange"
	"shopnexus-server/internal/shared/pgsqlc"
)

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

// NewCommonBiz creates a Restate-backed client for the common module.
func NewCommonBiz(cfg *commonconfig.Config) commonbiz.CommonBizClient {
	return commonbiz.NewCommonRestateClient(cfg.Restate.IngressAddress)
}

// NewExchangeClient provides a CurrencyAPI-backed exchange.Client
// configured from app settings. Chosen over Frankfurter for full ISO 4217
// coverage (VND, COP, CLP etc. that ECB-based providers don't ship).
func NewExchangeClient(cfg *commonconfig.Config) exchange.Client {
	return exchange.NewCurrencyAPI(
		cfg.Exchange.UpstreamURL,
		cfg.Exchange.APIKey,
		&http.Client{Timeout: cfg.Exchange.HTTPTimeout},
	)
}
