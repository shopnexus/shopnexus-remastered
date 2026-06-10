package inventorybiz

import (
	"context"
	"fmt"

	"shopnexus-server/internal/infras/metrics"
	inventorydb "shopnexus-server/internal/module/inventory/db/sqlc"
	inventorymodel "shopnexus-server/internal/module/inventory/model"
	"shopnexus-server/internal/shared/idempotency"

	"github.com/google/uuid"
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

func (b *InventoryHandler) ReleaseInventory(ctx context.Context, params ReleaseInventoryParams) (err error) {
	defer metrics.TrackHandler("inventory", "ReleaseInventory", &err)()

	txStorage, err := b.storage.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer txStorage.Rollback(ctx)

	if err = params.Keys.Apply(ctx, txStorage.Querier()); err != nil {
		return fmt.Errorf("check idempotency keys: %w", err)
	}

	for _, item := range params.Items {
		rows, e := txStorage.Querier().ReleaseInventory(ctx, inventorydb.ReleaseInventoryParams{
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

	if err = txStorage.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
