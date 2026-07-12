package inventory

import (
	restate "github.com/restatedev/sdk-go"
	"go.uber.org/fx"

	"shopnexus-server/config"
	"shopnexus-server/internal/infras/infra"
	inventorybiz "shopnexus-server/internal/module/inventory/biz"
	inventorydb "shopnexus-server/internal/module/inventory/db/sqlc"
	inventoryecho "shopnexus-server/internal/module/inventory/transport/echo"
	"shopnexus-server/internal/shared/besteffort"
	"shopnexus-server/internal/shared/pgsqlc"
	"shopnexus-server/internal/shared/restatesvc"
)

// Module provides the inventory module. Infra is its own fx.Private set via
// infra.StandardModule, built from the shared config.
var Module = fx.Module("inventory",
	infra.StandardModule("inventory"),
	fx.Provide(
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
func NewInventoryBiz(cfg *config.Config, biz *inventorybiz.InventoryHandler) inventorybiz.InventoryBizClient {
	return inventorybiz.NewInventoryBizClientInProcess(cfg.Restate.IngressAddress, biz)
}
