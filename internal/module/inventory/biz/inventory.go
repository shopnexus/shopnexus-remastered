package inventorybiz

import (
	"context"
	"fmt"

	"shopnexus-server/internal/infras/metrics"
	inventorydb "shopnexus-server/internal/module/inventory/db/sqlc"
	inventorymodel "shopnexus-server/internal/module/inventory/model"
	"shopnexus-server/internal/shared/idempotency"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	restate "github.com/restatedev/sdk-go"
	"github.com/samber/lo"
)

// refTypeLabel returns a lowercase human label for user-facing error messages.
// We avoid string(refType) so the DB enum casing ("ProductSku") doesn't leak to the UI.
func refTypeLabel(t inventorydb.InventoryStockRefType) string {
	switch t {
	case inventorydb.InventoryStockRefTypeProductSku:
		return "product"
	case inventorydb.InventoryStockRefTypePromotion:
		return "promotion"
	default:
		return "item"
	}
}

// getStockByRef is a shared helper to look up stock by (ref_type, ref_id).
func (b *InventoryHandler) getStockByRef(
	ctx context.Context,
	store InventoryStorage,
	refType inventorydb.InventoryStockRefType,
	refID uuid.UUID,
) (inventorydb.InventoryStock, error) {
	return store.Querier().GetStock(ctx, inventorydb.GetStockParams{
		RefID:   uuid.NullUUID{UUID: refID, Valid: true},
		RefType: inventorydb.NullInventoryStockRefType{InventoryStockRefType: refType, Valid: true},
	})
}

type GetStockParams struct {
	RefID   uuid.UUID                         `validate:"required"`
	RefType inventorydb.InventoryStockRefType `validate:"required,validateFn=Valid"`
}

// GetStock returns stock info for the given reference type and ID.
func (b *InventoryHandler) GetStock(ctx context.Context, params GetStockParams) (inventorydb.InventoryStock, error) {
	var zero inventorydb.InventoryStock
	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate get stock: %w", err)
	}
	stock, err := b.getStockByRef(ctx, b.storage, params.RefType, params.RefID)
	if err != nil {
		return zero, fmt.Errorf("db get stock: %w", err)
	}
	return stock, nil
}

type UpdateStockSettingsParams struct {
	RefID          uuid.UUID                         `validate:"required"`
	RefType        inventorydb.InventoryStockRefType `validate:"required,validateFn=Valid"`
	SerialRequired null.Bool                         `validate:"omitnil"`
}

// UpdateStockSettings updates stock settings like serial_required.
func (b *InventoryHandler) UpdateStockSettings(
	ctx restate.Context,
	params UpdateStockSettingsParams,
) (inventorydb.InventoryStock, error) {
	var zero inventorydb.InventoryStock
	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate update stock settings: %w", err)
	}

	// execution: load the stock, then update its settings.
	return restate.Run(ctx, func(rctx restate.RunContext) (inventorydb.InventoryStock, error) {
		stock, err := b.getStockByRef(rctx, b.storage, params.RefType, params.RefID)
		if err != nil {
			return zero, fmt.Errorf("db get stock: %w", err)
		}

		updated, err := b.storage.Querier().UpdateStock(rctx, inventorydb.UpdateStockParams{
			ID:             stock.ID,
			SerialRequired: params.SerialRequired,
		})
		if err != nil {
			return zero, fmt.Errorf("db update stock: %w", err)
		}
		return updated, nil
	})
}

type ListStockParams struct {
	paginate.Params

	RefType []inventorydb.InventoryStockRefType `validate:"dive,required,validateFn=Valid"`
	RefID   []uuid.UUID                         `validate:"dive,required"`
}

// ListStock returns a paginated list of stock records filtered by ref type and ID.
func (b *InventoryHandler) ListStock(
	ctx context.Context,
	params ListStockParams,
) (paginate.PaginateResult[inventorydb.InventoryStock], error) {
	var zero paginate.PaginateResult[inventorydb.InventoryStock]
	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate list stock: %w", err)
	}

	res, err := b.storage.Querier().ListStock(ctx, inventorydb.ListStockParams{Params: params.Params,
		RefType: params.RefType,
		RefId:   params.RefID,
	})
	if err != nil {
		return zero, fmt.Errorf("db list stock: %w", err)
	}

	return paginate.PaginateResult[inventorydb.InventoryStock]{
		PageParams: res.PageParams,
		Total:      res.Total,
		Data:       res.Data,
	}, nil
}

type CreateStockParams struct {
	RefID          uuid.UUID                         `validate:"required"`
	RefType        inventorydb.InventoryStockRefType `validate:"required,validateFn=Valid"`
	Stock          int64                             `validate:"gte=0"`
	SerialRequired bool                              `validate:"omitempty"`
}

// CreateStock creates a new stock record for the given reference.
func (b *InventoryHandler) CreateStock(
	ctx restate.Context,
	params CreateStockParams,
) (inventorydb.InventoryStock, error) {
	var zero inventorydb.InventoryStock
	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate create stock: %w", err)
	}

	// execution: create the default stock record.
	return restate.Run(ctx, func(rctx restate.RunContext) (inventorydb.InventoryStock, error) {
		created, err := b.storage.Querier().CreateDefaultStock(rctx, inventorydb.CreateDefaultStockParams{
			RefType:        params.RefType,
			RefID:          params.RefID,
			Stock:          params.Stock,
			SerialRequired: params.SerialRequired,
		})
		if err != nil {
			return zero, fmt.Errorf("db create default stock: %w", err)
		}
		return created, nil
	})
}

type ListStockHistoryParams struct {
	paginate.Params

	RefID   uuid.UUID                         `validate:"required"`
	RefType inventorydb.InventoryStockRefType `validate:"required,validateFn=Valid"`
}

// ListStockHistory returns a paginated list of stock change history for the given reference.
func (b *InventoryHandler) ListStockHistory(
	ctx context.Context,
	params ListStockHistoryParams,
) (paginate.PaginateResult[inventorydb.InventoryStockHistory], error) {
	var zero paginate.PaginateResult[inventorydb.InventoryStockHistory]
	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate list stock history: %w", err)
	}

	stock, err := b.getStockByRef(ctx, b.storage, params.RefType, params.RefID)
	if err != nil {
		return zero, fmt.Errorf("db get stock: %w", err)
	}

	res, err := b.storage.Querier().ListStockHistory(ctx, inventorydb.ListStockHistoryParams{Params: params.Params,
		StockId: []int64{stock.ID},
	})
	if err != nil {
		return zero, fmt.Errorf("db list stock history: %w", err)
	}

	return paginate.PaginateResult[inventorydb.InventoryStockHistory]{
		PageParams: res.PageParams,
		Total:      res.Total,
		Data:       res.Data,
	}, nil
}

type ImportStockParams struct {
	RefID     uuid.UUID                         `validate:"required"`
	RefType   inventorydb.InventoryStockRefType `validate:"required,validateFn=Valid"`
	Change    int64                             `validate:"required,gt=0"`
	SerialIDs []string                          `validate:"dive,required"`
}

// ImportStock adds stock quantity and optionally creates serial records.
func (b *InventoryHandler) ImportStock(ctx restate.Context, params ImportStockParams) error {
	if err := validator.Validate(params); err != nil {
		return fmt.Errorf("validate import stock: %w", err)
	}

	// decision: load the target stock.
	stock, err := restate.Run(ctx, func(rctx restate.RunContext) (inventorydb.InventoryStock, error) {
		stock, err := b.getStockByRef(rctx, b.storage, params.RefType, params.RefID)
		if err != nil {
			return inventorydb.InventoryStock{}, fmt.Errorf("db get stock: %w", err)
		}
		return stock, nil
	})
	if err != nil {
		return err
	}

	// execution: record history, mint serials for serialized stock, bump current stock.
	return restate.RunVoid(ctx, func(rctx restate.RunContext) error {
		q := b.storage.Querier()

		if _, err := q.CreateDefaultStockHistory(rctx, inventorydb.CreateDefaultStockHistoryParams{
			StockID: stock.ID,
			Change:  params.Change,
		}); err != nil {
			return fmt.Errorf("db create stock history: %w", err)
		}

		// Create serials for serialized stock
		if stock.SerialRequired {
			var args []inventorydb.CreateCopyDefaultSerialParams

			if len(params.SerialIDs) != 0 {
				if len(params.SerialIDs) != int(params.Change) {
					return inventorymodel.ErrSerialCountMismatch
				}
				for _, id := range params.SerialIDs {
					args = append(args, inventorydb.CreateCopyDefaultSerialParams{
						ID:      id,
						StockID: stock.ID,
					})
				}
			} else {
				for range params.Change {
					args = append(args, inventorydb.CreateCopyDefaultSerialParams{
						ID:      uuid.NewString(),
						StockID: stock.ID,
					})
				}
			}

			if _, err := q.CreateCopyDefaultSerial(rctx, args); err != nil {
				return fmt.Errorf("db create serials: %w", err)
			}
		}

		if err := q.UpdateCurrentStock(rctx, inventorydb.UpdateCurrentStockParams{
			ID:     stock.ID,
			Change: params.Change,
		}); err != nil {
			return fmt.Errorf("db update current stock: %w", err)
		}
		return nil
	})
}

type ReserveInventoryItem struct {
	RefType inventorydb.InventoryStockRefType
	RefID   uuid.UUID
	Amount  int64
	// DisplayName is an optional user-facing label surfaced in error messages
	// (e.g. the SPU name). Empty falls back to the generic refTypeLabel.
	DisplayName string
}

type ReserveInventoryResult struct {
	SerialIDs []string
	RefType   inventorydb.InventoryStockRefType
	RefID     uuid.UUID
}

type ReserveInventoryParams struct {
	idempotency.Keys
	Items []ReserveInventoryItem
}

// ReserveInventory reserves stock for the given items and assigns serial IDs when required.
func (b *InventoryHandler) ReserveInventory(
	ctx restate.Context,
	params ReserveInventoryParams,
) (results []ReserveInventoryResult, err error) {
	defer metrics.TrackHandler("inventory", "ReserveInventory", &err)()
	defer func() {
		result := "success"
		if err != nil {
			result = "failure"
		}
		metrics.InventoryReservesTotal.WithLabelValues(result).Add(float64(len(params.Items)))
	}()

	// execution: single-module atomic reserve. Idempotency keys + stock adjust + serial
	// assignment all commit together; nothing crosses module boundaries, so no saga.
	results, err = restate.Run(ctx, func(rctx restate.RunContext) ([]ReserveInventoryResult, error) {
		txStorage, err := b.storage.BeginTx(rctx)
		if err != nil {
			return nil, fmt.Errorf("begin transaction: %w", err)
		}
		defer txStorage.Rollback(rctx)

		if err = params.Keys.Apply(rctx, txStorage.Querier()); err != nil {
			return nil, fmt.Errorf("check idempotency keys: %w", err)
		}

		var out []ReserveInventoryResult
		for _, item := range params.Items {
			var stock inventorydb.InventoryStock
			stock, err = b.getStockByRef(rctx, txStorage, item.RefType, item.RefID)
			if err != nil {
				return nil, fmt.Errorf("db get stock: %w", err)
			}

			label := item.DisplayName
			if label == "" {
				label = refTypeLabel(item.RefType)
			}

			if stock.Stock < item.Amount {
				return nil, inventorymodel.ErrOutOfStock.Fmt(label, item.Amount, stock.Stock)
			}

			var rowsAffected int64
			rowsAffected, err = txStorage.Querier().AdjustInventory(rctx, inventorydb.AdjustInventoryParams{
				StockID: stock.ID,
				Amount:  item.Amount,
			})
			if err != nil {
				return nil, fmt.Errorf("db adjust inventory: %w", err)
			}
			if rowsAffected == 0 {
				return nil, inventorymodel.ErrOutOfStockRace.Fmt(label)
			}

			result := ReserveInventoryResult{
				RefType: item.RefType,
				RefID:   item.RefID,
			}

			if stock.SerialRequired {
				var serials []inventorydb.GetAvailableSerialsRow
				serials, err = txStorage.Querier().GetAvailableSerials(rctx, inventorydb.GetAvailableSerialsParams{
					StockID: stock.ID,
					Amount:  int32(item.Amount),
				})
				if err != nil {
					return nil, fmt.Errorf("db get available serials: %w", err)
				}

				if len(serials) != int(item.Amount) {
					return nil, inventorymodel.ErrSerialShortage.Fmt(len(serials), label, item.Amount)
				}

				serialIDs := lo.Map(serials, func(row inventorydb.GetAvailableSerialsRow, _ int) string {
					return row.ID
				})

				if err = txStorage.Querier().UpdateSerialStatus(rctx, inventorydb.UpdateSerialStatusParams{
					ID:     serialIDs,
					Status: inventorydb.InventoryStatusTaken,
				}); err != nil {
					return nil, fmt.Errorf("db update serial status: %w", err)
				}

				result.SerialIDs = serialIDs
			}

			out = append(out, result)
		}

		if err = txStorage.Commit(rctx); err != nil {
			return nil, fmt.Errorf("commit transaction: %w", err)
		}

		return out, nil
	})
	return results, err
}

type UpdateSerialParams struct {
	SerialIDs []string
	Status    inventorydb.InventoryStatus `validate:"required,validateFn=Valid"`
}

// UpdateSerial updates the status of the given serial IDs.
func (b *InventoryHandler) UpdateSerial(ctx restate.Context, params UpdateSerialParams) error {
	if err := validator.Validate(params); err != nil {
		return fmt.Errorf("validate update serial: %w", err)
	}

	// execution: update serial statuses.
	return restate.RunVoid(ctx, func(rctx restate.RunContext) error {
		if err := b.storage.Querier().UpdateSerialStatus(rctx, inventorydb.UpdateSerialStatusParams{
			ID:     params.SerialIDs,
			Status: params.Status,
		}); err != nil {
			return fmt.Errorf("db update serial status: %w", err)
		}
		return nil
	})
}

type ListSerialParams struct {
	paginate.Params

	StockID int64 `validate:"required,gt=0"`
}

// ListSerial returns a paginated list of serials for the given stock ID.
func (b *InventoryHandler) ListSerial(
	ctx context.Context,
	params ListSerialParams,
) (paginate.PaginateResult[inventorydb.InventorySerial], error) {
	var zero paginate.PaginateResult[inventorydb.InventorySerial]
	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate list serial: %w", err)
	}

	res, err := b.storage.Querier().ListSerial(ctx, inventorydb.ListSerialParams{Params: params.Params,
		StockId: []int64{params.StockID},
	})
	if err != nil {
		return zero, fmt.Errorf("db list serial: %w", err)
	}

	return paginate.PaginateResult[inventorydb.InventorySerial]{
		PageParams: res.PageParams,
		Total:      res.Total,
		Data:       res.Data,
	}, nil
}

type ListMostTakenSkuParams struct {
	paginate.Params

	RefType inventorydb.InventoryStockRefType `validate:"required,validateFn=Valid"`
}

// ListMostTakenSku returns the most reserved SKUs ordered by taken count.
func (b *InventoryHandler) ListMostTakenSku(
	ctx context.Context,
	params ListMostTakenSkuParams,
) ([]inventorydb.InventoryStock, error) {
	if err := validator.Validate(params); err != nil {
		return nil, fmt.Errorf("validate list most taken: %w", err)
	}

	stocks, err := b.storage.Querier().ListMostTakenSku(ctx, inventorydb.ListMostTakenSkuParams{
		Limit:   params.Limit,
		Offset:  params.Offset(),
		RefType: params.RefType,
	})
	if err != nil {
		return nil, fmt.Errorf("db list most taken sku: %w", err)
	}
	return stocks, nil
}
