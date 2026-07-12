package analytic

import (
	restate "github.com/restatedev/sdk-go"
	"go.uber.org/fx"

	"shopnexus-server/config"
	"shopnexus-server/internal/infras/infra"
	analyticbiz "shopnexus-server/internal/module/analytic/biz"
	analyticdb "shopnexus-server/internal/module/analytic/db/sqlc"
	analyticecho "shopnexus-server/internal/module/analytic/transport/echo"
	analyticworkers "shopnexus-server/internal/module/analytic/workers"
	"shopnexus-server/internal/shared/besteffort"
	"shopnexus-server/internal/shared/pgsqlc"
	"shopnexus-server/internal/shared/restatesvc"
)

// Module provides the analytic module. Infra is its own fx.Private set via
// infra.StandardModule, built from the shared config.
var Module = fx.Module("analytic",
	infra.StandardModule("analytic"),
	fx.Provide(
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
func NewAnalyticBiz(cfg *config.Config, biz *analyticbiz.AnalyticHandler) analyticbiz.AnalyticBizClient {
	return analyticbiz.NewAnalyticBizClientInProcess(cfg.Restate.IngressAddress, biz)
}
