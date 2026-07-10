package analytic

import (
	"log/slog"

	restate "github.com/restatedev/sdk-go"
	"go.uber.org/fx"

	"shopnexus-server/internal/infras/bus"
	"shopnexus-server/internal/infras/cache"
	"shopnexus-server/internal/infras/infra"
	"shopnexus-server/internal/infras/rankedset"
	analyticbiz "shopnexus-server/internal/module/analytic/biz"
	analyticconfig "shopnexus-server/internal/module/analytic/config"
	analyticdb "shopnexus-server/internal/module/analytic/db/sqlc"
	analyticecho "shopnexus-server/internal/module/analytic/transport/echo"
	analyticworkers "shopnexus-server/internal/module/analytic/workers"
	"shopnexus-server/internal/shared/besteffort"
	"shopnexus-server/internal/shared/pgsqlc"
	"shopnexus-server/internal/shared/restatesvc"
)

// Module provides the analytic module dependencies. The pool/cache/logger
// providers are fx.Private — each is constructed from THIS module's own
// Postgres/Redis/Log config and is invisible to other modules' fx graphs,
// so 8 modules can each `Provide(... pgsqlc.TxBeginner ...)` without
// colliding. Cache is provided for parity with the other modules even though
// analytic biz currently doesn't consume it.
var Module = fx.Module("analytic",
	fx.Provide(
		func(c *analyticconfig.Config) *slog.Logger { return infra.NewLogger(c.Log, "analytic") },
		func(c *analyticconfig.Config, lc fx.Lifecycle) (pgsqlc.TxBeginner, error) {
			return infra.NewPool(c.Postgres, lc)
		},
		func(c *analyticconfig.Config, lc fx.Lifecycle) (cache.Client, error) {
			return infra.NewCache(c.Redis, lc)
		},
		func(c *analyticconfig.Config, logger *slog.Logger, lc fx.Lifecycle) (bus.Client, error) {
			return infra.NewBus(c.Bus, c.Redis, logger, lc)
		},
		func(c *analyticconfig.Config, lc fx.Lifecycle) (rankedset.Client, error) {
			return infra.NewRankedSet(c.RankedSet, c.Redis, lc)
		},
		fx.Private,
	),
	fx.Provide(
		analyticconfig.NewConfig,
		NewAnalyticStorage,
		analyticbiz.NewAnalyticHandler,
		NewAnalyticBiz,
		analyticecho.NewHandler,
	),
	fx.Provide(
		fx.Annotate(
			func(b *analyticbiz.AnalyticHandler) restate.ServiceDefinition {
				return restatesvc.Reflect(analyticbiz.NewAnalyticService(b))
			},
			fx.ResultTags(`group:"restate"`),
		),
		fx.Annotate(
			func(b *analyticbiz.AnalyticHandler) besteffort.Registrar {
				return func(s *besteffort.Server) { analyticbiz.RegisterAnalyticBestEffort(s, b) }
			},
			fx.ResultTags(`group:"besteffort"`),
		),
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
