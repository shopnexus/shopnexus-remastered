package inventory

import (
	"go.uber.org/fx"

	"shopnexus-server/internal/infras/fxinfra"
	inventorybiz "shopnexus-server/internal/module/inventory/biz"
	inventoryconfig "shopnexus-server/internal/module/inventory/config"
	inventorydb "shopnexus-server/internal/module/inventory/db/sqlc"
	inventoryecho "shopnexus-server/internal/module/inventory/transport/echo"
	"shopnexus-server/internal/shared/pgsqlc"
)

// Module provides the inventory module dependencies. The pool/cache/logger
// providers are fx.Private — each is constructed from THIS module's own
// Postgres/Redis/Log config and is invisible to other modules' fx graphs,
// so 8 modules can each `Provide(... pgsqlc.TxBeginner ...)` without
// colliding.
var Module = fx.Module("inventory",
	fxinfra.Providers[*inventoryconfig.Config]("inventory"),
	fx.Provide(
		inventoryconfig.NewConfig,
		NewInventoryStorage,
		inventorybiz.NewInventoryBiz,
		NewInventoryBiz,
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
