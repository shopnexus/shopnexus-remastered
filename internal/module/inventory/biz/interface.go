package inventorybiz

import (
	"context"

	inventorydb "shopnexus-server/internal/module/inventory/db/sqlc"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/pgsqlc"
)

// InventoryBiz is the client interface for InventoryBizHandler, which is used by other modules to call InventoryBizHandler methods.
//
//go:generate go run shopnexus-server/cmd/genrestate -interface InventoryBiz -service Inventory
type InventoryBiz interface {
	// Stock
	GetStock(ctx context.Context, params GetStockParams) (inventorydb.InventoryStock, error)
	UpdateStockSettings(ctx context.Context, params UpdateStockSettingsParams) (inventorydb.InventoryStock, error)
	ListStock(
		ctx context.Context,
		params ListStockParams,
	) (paginate.PaginateResult[inventorydb.InventoryStock], error)
	CreateStock(ctx context.Context, params CreateStockParams) (inventorydb.InventoryStock, error)

	// Stock History
	ListStockHistory(
		ctx context.Context,
		params ListStockHistoryParams,
	) (paginate.PaginateResult[inventorydb.InventoryStockHistory], error)

	// Import
	ImportStock(ctx context.Context, params ImportStockParams) error

	// Reserve
	ReserveInventory(ctx context.Context, params ReserveInventoryParams) ([]ReserveInventoryResult, error)
	ReleaseInventory(ctx context.Context, params ReleaseInventoryParams) error

	// Serial
	UpdateSerial(ctx context.Context, params UpdateSerialParams) error
	ListSerial(
		ctx context.Context,
		params ListSerialParams,
	) (paginate.PaginateResult[inventorydb.InventorySerial], error)

	// Most Taken
	ListMostTakenSku(ctx context.Context, params ListMostTakenSkuParams) ([]inventorydb.InventoryStock, error)
}

type InventoryStorage = pgsqlc.Storage[*inventorydb.Queries]

// InventoryHandler implements the core business logic for the inventory module.
type InventoryHandler struct {
	storage InventoryStorage
}

func (h *InventoryHandler) ServiceName() string {
	return "Inventory"
}

// NewInventoryBiz creates a new InventoryHandler with the given dependencies.
func NewInventoryBiz(storage InventoryStorage) *InventoryHandler {
	return &InventoryHandler{storage: storage}
}
