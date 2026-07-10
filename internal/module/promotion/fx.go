package promotion

import (
	"log/slog"

	restate "github.com/restatedev/sdk-go"
	"go.uber.org/fx"

	"shopnexus-server/internal/infras/bus"
	"shopnexus-server/internal/infras/cache"
	"shopnexus-server/internal/infras/infra"
	"shopnexus-server/internal/infras/rankedset"
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
	fx.Provide(
		func(c *promotionconfig.Config) *slog.Logger { return infra.NewLogger(c.Log, "promotion") },
		func(c *promotionconfig.Config, lc fx.Lifecycle) (pgsqlc.TxBeginner, error) {
			return infra.NewPool(c.Postgres, lc)
		},
		func(c *promotionconfig.Config, lc fx.Lifecycle) (cache.Client, error) {
			return infra.NewCache(c.Redis, lc)
		},
		func(c *promotionconfig.Config, logger *slog.Logger, lc fx.Lifecycle) (bus.Client, error) {
			return infra.NewBus(c.Bus, c.Redis, logger, lc)
		},
		func(c *promotionconfig.Config, lc fx.Lifecycle) (rankedset.Client, error) {
			return infra.NewRankedSet(c.RankedSet, c.Redis, lc)
		},
		fx.Private,
	),
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
