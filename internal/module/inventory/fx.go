package inventory

import (
	inventorybiz "shopnexus-remastered/internal/module/inventory/biz"
	inventoryecho "shopnexus-remastered/internal/module/inventory/transport/echo"

	"go.uber.org/fx"
)

// Module provides the inventory module dependencies
var Module = fx.Module("inventory",
	fx.Provide(
		inventorybiz.NewInventoryBiz,
		inventoryecho.NewHandler,
	),
)
