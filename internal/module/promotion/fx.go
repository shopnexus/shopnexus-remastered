package promotion

import (
	restate "github.com/restatedev/sdk-go"
	"go.uber.org/fx"

	"shopnexus-server/internal/infras/fxinfra"
	promotionbiz "shopnexus-server/internal/module/promotion/biz"
	promotionconfig "shopnexus-server/internal/module/promotion/config"
	promotiondb "shopnexus-server/internal/module/promotion/db/sqlc"
	promotionecho "shopnexus-server/internal/module/promotion/transport/echo"
	"shopnexus-server/internal/shared/besteffort"
	"shopnexus-server/internal/shared/pgsqlc"
	"shopnexus-server/internal/shared/restatesvc"
)

// Module provides the promotion module dependencies. The pool/cache/logger
// providers are fx.Private — each is constructed from THIS module's own
// Postgres/Redis/Log config and is invisible to other modules' fx graphs,
// so 8 modules can each `Provide(... pgsqlc.TxBeginner ...)` without
// colliding.
var Module = fx.Module("promotion",
	fxinfra.Providers[*promotionconfig.Config]("promotion"),
	fx.Provide(
		promotionconfig.NewConfig,
		NewPromotionStorage,
		promotionbiz.NewPromotionHandler,
		NewPromotionBiz,
		promotionecho.NewHandler,
	),
	fx.Provide(
		fx.Annotate(
			func(b *promotionbiz.PromotionHandler) restate.ServiceDefinition {
				return restatesvc.Reflect(promotionbiz.NewPromotionService(b))
			},
			fx.ResultTags(`group:"restate"`),
		),
		fx.Annotate(
			func(b *promotionbiz.PromotionHandler) besteffort.Registrar {
				return func(s *besteffort.Server) { promotionbiz.RegisterPromotionBestEffort(s, b) }
			},
			fx.ResultTags(`group:"besteffort"`),
		),
	),
	fx.Invoke(
		promotionecho.NewHandler,
	),
)

// NewPromotionStorage creates a new promotion storage backed by PostgreSQL.
func NewPromotionStorage(pool pgsqlc.TxBeginner) promotionbiz.PromotionStorage {
	return pgsqlc.NewStorage(pool, promotiondb.New(pool))
}

// NewPromotionBiz creates the promotion client. BestEffort calls run in-process.
func NewPromotionBiz(cfg *promotionconfig.Config, biz *promotionbiz.PromotionHandler) promotionbiz.PromotionBizClient {
	return promotionbiz.NewPromotionBizClientInProcess(cfg.Restate.IngressAddress, biz)
}
