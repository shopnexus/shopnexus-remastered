package promotion

import (
	restate "github.com/restatedev/sdk-go"
	"go.uber.org/fx"

	"shopnexus-server/config"
	"shopnexus-server/internal/infras/infra"
	promotionbiz "shopnexus-server/internal/module/promotion/biz"
	promotiondb "shopnexus-server/internal/module/promotion/db/sqlc"
	promotionecho "shopnexus-server/internal/module/promotion/transport/echo"
	"shopnexus-server/internal/shared/besteffort"
	"shopnexus-server/internal/shared/pgsqlc"
	"shopnexus-server/internal/shared/restatesvc"
)

// Module provides the promotion module. Infra is its own fx.Private set via
// infra.StandardModule, built from the shared config.
var Module = fx.Module("promotion",
	infra.StandardModule("promotion"),
	fx.Provide(
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
func NewPromotionBiz(cfg *config.Config, biz *promotionbiz.PromotionHandler) promotionbiz.PromotionBizClient {
	return promotionbiz.NewPromotionBizClientInProcess(cfg.Restate.IngressAddress, biz)
}
