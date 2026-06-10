package analytic

import (
	"go.uber.org/fx"

	"shopnexus-server/internal/infras/fxinfra"
	analyticbiz "shopnexus-server/internal/module/analytic/biz"
	analyticconfig "shopnexus-server/internal/module/analytic/config"
	analyticdb "shopnexus-server/internal/module/analytic/db/sqlc"
	analyticecho "shopnexus-server/internal/module/analytic/transport/echo"
	analyticworkers "shopnexus-server/internal/module/analytic/workers"
	"shopnexus-server/internal/shared/pgsqlc"
)

// Module provides the analytic module dependencies. The pool/cache/logger
// providers are fx.Private — each is constructed from THIS module's own
// Postgres/Redis/Log config and is invisible to other modules' fx graphs,
// so 8 modules can each `Provide(... pgsqlc.TxBeginner ...)` without
// colliding. Cache is provided for parity with the other modules even though
// analytic biz currently doesn't consume it.
var Module = fx.Module("analytic",
	fxinfra.Providers[*analyticconfig.Config]("analytic"),
	fx.Provide(
		analyticconfig.NewConfig,
		NewAnalyticStorage,
		analyticbiz.NewAnalyticHandler,
		NewAnalyticBiz,
		analyticecho.NewHandler,
	),
	fx.Invoke(
		analyticecho.NewHandler,
		analyticworkers.Register,
	),
)

// NewAnalyticStorage creates a new analytic storage backed by PostgreSQL.
func NewAnalyticStorage(pool pgsqlc.TxBeginner) analyticbiz.AnalyticStorage {
	return pgsqlc.NewStorage(pool, analyticdb.New(pool))
}

// NewAnalyticBiz creates the analytic client. BestEffort calls run in-process.
func NewAnalyticBiz(cfg *analyticconfig.Config, biz *analyticbiz.AnalyticHandler) analyticbiz.AnalyticBizClient {
	return analyticbiz.NewAnalyticBizClientInProcess(cfg.Restate.IngressAddress, biz)
}
