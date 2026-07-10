package inventory

import (
	"log/slog"

	restate "github.com/restatedev/sdk-go"
	"go.uber.org/fx"

	"shopnexus-server/internal/infras/bus"
	"shopnexus-server/internal/infras/cache"
	"shopnexus-server/internal/infras/infra"
	"shopnexus-server/internal/infras/rankedset"
	inventorybiz "shopnexus-server/internal/module/inventory/biz"
	inventoryconfig "shopnexus-server/internal/module/inventory/config"
	inventorydb "shopnexus-server/internal/module/inventory/db/sqlc"
	inventoryecho "shopnexus-server/internal/module/inventory/transport/echo"
	"shopnexus-server/internal/shared/besteffort"
	"shopnexus-server/internal/shared/pgsqlc"
	"shopnexus-server/internal/shared/restatesvc"
)

// Module provides the inventory module dependencies. The pool/cache/logger
// providers are fx.Private — each is constructed from THIS module's own
// Postgres/Redis/Log config and is invisible to other modules' fx graphs,
// so 8 modules can each `Provide(... pgsqlc.TxBeginner ...)` without
// colliding.
var Module = fx.Module("inventory",
	fx.Provide(
		func(c *inventoryconfig.Config) *slog.Logger { return infra.NewLogger(c.Log, "inventory") },
		func(c *inventoryconfig.Config, lc fx.Lifecycle) (pgsqlc.TxBeginner, error) {
			return infra.NewPool(c.Postgres, lc)
		},
		func(c *inventoryconfig.Config, lc fx.Lifecycle) (cache.Client, error) {
			return infra.NewCache(c.Redis, lc)
		},
		func(c *inventoryconfig.Config, logger *slog.Logger, lc fx.Lifecycle) (bus.Client, error) {
			return infra.NewBus(c.Bus, c.Redis, logger, lc)
		},
		func(c *inventoryconfig.Config, lc fx.Lifecycle) (rankedset.Client, error) {
			return infra.NewRankedSet(c.RankedSet, c.Redis, lc)
		},
		fx.Private,
	),
	fx.Provide(
		inventoryconfig.NewConfig,
		NewInventoryStorage,
		inventorybiz.NewInventoryBiz,
		NewInventoryBiz,
	),
	fx.Provide(
		fx.Annotate(
			func(b *inventorybiz.InventoryHandler) restate.ServiceDefinition {
				return restatesvc.Reflect(inventorybiz.NewInventoryService(b))
			},
			fx.ResultTags(`group:"restate"`),
		),
		fx.Annotate(
			func(b *inventorybiz.InventoryHandler) besteffort.Registrar {
				return func(s *besteffort.Server) { inventorybiz.RegisterInventoryBestEffort(s, b) }
			},
			fx.ResultTags(`group:"besteffort"`),
		),
	),
	fx.Invoke(
		inventoryecho.NewHandler,
	),
)

// NewInventoryStorage creates a new inventory storage backed by PostgreSQL.
func NewInventoryStorage(pool pgsqlc.TxBeginner) inventorybiz.InventoryStorage {
	return pgsqlc.NewStorage(pool, inventorydb.New(pool))
}

// NewInventoryBiz creates the inventory client. BestEffort calls run in-process.
func NewInventoryBiz(cfg *inventoryconfig.Config, biz *inventorybiz.InventoryHandler) inventorybiz.InventoryBizClient {
	return inventorybiz.NewInventoryBizClientInProcess(cfg.Restate.IngressAddress, biz)
}
