package inventorybiz

import (
	"fmt"

	"shopnexus-server/internal/infras/metrics"
	inventorydb "shopnexus-server/internal/module/inventory/db/sqlc"
	inventorymodel "shopnexus-server/internal/module/inventory/model"
	"shopnexus-server/internal/shared/idempotency"

	"github.com/google/uuid"
	restate "github.com/restatedev/sdk-go"
)

type ReleaseInventoryParams struct {
	idempotency.Keys
	Items []ReleaseInventoryItem
}

type ReleaseInventoryItem struct {
	RefType inventorydb.InventoryStockRefType
	RefID   uuid.UUID
	Amount  int64
}

func (b *InventoryHandler) ReleaseInventory(ctx restate.Context, params ReleaseInventoryParams) (err error) {
	defer metrics.TrackHandler("inventory", "ReleaseInventory", &err)()

	// execution: single-module atomic release. Idempotency keys + release writes commit
	// together; nothing crosses module boundaries, so no saga.
	err = restate.RunVoid(ctx, func(rctx restate.RunContext) error {
		txStorage, err := b.storage.BeginTx(rctx)
		if err != nil {
			return fmt.Errorf("begin transaction: %w", err)
		}
		defer txStorage.Rollback(rctx)

		if err = params.Keys.Apply(rctx, txStorage.Querier()); err != nil {
			return fmt.Errorf("check idempotency keys: %w", err)
		}

		for _, item := range params.Items {
			rows, e := txStorage.Querier().ReleaseInventory(rctx, inventorydb.ReleaseInventoryParams{
				RefID:   item.RefID,
				RefType: item.RefType,
				Amount:  item.Amount,
			})
			if e != nil {
				return fmt.Errorf("release inventory: %w", e)
			}
			if rows == 0 {
				return inventorymodel.ErrInsufficientReservedInventory
			}
		}

		if err = txStorage.Commit(rctx); err != nil {
			return fmt.Errorf("commit transaction: %w", err)
		}
		return nil
	})
	return err
}
