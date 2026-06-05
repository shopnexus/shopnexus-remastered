package inventorybiz

import (
	"fmt"

	restate "github.com/restatedev/sdk-go"

	"shopnexus-server/internal/infras/metrics"
	inventorydb "shopnexus-server/internal/module/inventory/db/sqlc"
	inventorymodel "shopnexus-server/internal/module/inventory/model"
	"shopnexus-server/internal/shared/idempotency"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
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
	ctx restate.Context,
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
func (b *InventoryHandler) GetStock(ctx restate.Context, params GetStockParams) (inventorydb.InventoryStock, error) {
	var zero inventorydb.InventoryStock
	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate get stock: %w", err)
	}
	return b.getStockByRef(ctx, b.storage, params.RefType, params.RefID)
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

	stock, err := b.getStockByRef(ctx, b.storage, params.RefType, params.RefID)
	if err != nil {
		return zero, fmt.Errorf("db get stock: %w", err)
	}

	return b.storage.Querier().UpdateStock(ctx, inventorydb.UpdateStockParams{
		ID:             stock.ID,
		SerialRequired: params.SerialRequired,
	})
}

type ListStockParams struct {
	paginate.Params

	RefType []inventorydb.InventoryStockRefType `validate:"dive,required,validateFn=Valid"`
	RefID   []uuid.UUID                         `validate:"dive,required"`
}

// ListStock returns a paginated list of stock records filtered by ref type and ID.
func (b *InventoryHandler) ListStock(
	ctx restate.Context,
	params ListStockParams,
) (paginate.PaginateResult[inventorydb.InventoryStock], error) {
	var zero paginate.PaginateResult[inventorydb.InventoryStock]
	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate list stock: %w", err)
	}

	rows, err := b.storage.Querier().ListCountStock(ctx, inventorydb.ListCountStockParams{
		Limit:   params.Limit,
		Offset:  params.Offset(),
		RefType: params.RefType,
		RefID:   params.RefID,
	})
	if err != nil {
		return zero, fmt.Errorf("db list stock: %w", err)
	}

	var total null.Int64
	if len(rows) > 0 {
		total.SetValid(rows[0].TotalCount)
	}

	stocks := lo.Map(rows, func(r inventorydb.ListCountStockRow, _ int) inventorydb.InventoryStock {
		return r.InventoryStock
	})

	return paginate.PaginateResult[inventorydb.InventoryStock]{
		PageParams: params.Params,
		Total:      total,
		Data:       stocks,
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

	return b.storage.Querier().CreateDefaultStock(ctx, inventorydb.CreateDefaultStockParams{
		RefType:        params.RefType,
		RefID:          params.RefID,
		Stock:          params.Stock,
		SerialRequired: params.SerialRequired,
	})
}

type ListStockHistoryParams struct {
	paginate.Params

	RefID   uuid.UUID                         `validate:"required"`
	RefType inventorydb.InventoryStockRefType `validate:"required,validateFn=Valid"`
}

// ListStockHistory returns a paginated list of stock change history for the given reference.
func (b *InventoryHandler) ListStockHistory(
	ctx restate.Context,
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

	rows, err := b.storage.Querier().ListCountStockHistory(ctx, inventorydb.ListCountStockHistoryParams{
		StockID: []int64{stock.ID},
		Limit:   params.Limit,
		Offset:  params.Offset(),
	})
	if err != nil {
		return zero, fmt.Errorf("db list stock history: %w", err)
	}

	var total null.Int64
	if len(rows) > 0 {
		total.SetValid(rows[0].TotalCount)
	}

	histories := lo.Map(rows, func(r inventorydb.ListCountStockHistoryRow, _ int) inventorydb.InventoryStockHistory {
		return r.InventoryStockHistory
	})

	return paginate.PaginateResult[inventorydb.InventoryStockHistory]{
		PageParams: params.Params,
		Total:      total,
		Data:       histories,
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

	q := b.storage.Querier()

	stock, err := b.getStockByRef(ctx, b.storage, params.RefType, params.RefID)
	if err != nil {
		return fmt.Errorf("db get stock: %w", err)
	}

	if _, err := q.CreateDefaultStockHistory(ctx, inventorydb.CreateDefaultStockHistoryParams{
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

		if _, err = q.CreateCopyDefaultSerial(ctx, args); err != nil {
			return fmt.Errorf("db create serials: %w", err)
		}
	}

	return q.UpdateCurrentStock(ctx, inventorydb.UpdateCurrentStockParams{
		ID:     stock.ID,
		Change: params.Change,
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

	txStorage, err := b.storage.BeginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer txStorage.Rollback(ctx)

	if err = params.Keys.Apply(ctx, txStorage.Querier()); err != nil {
		return nil, fmt.Errorf("check idempotency keys: %w", err)
	}

	for _, item := range params.Items {
		var stock inventorydb.InventoryStock
		stock, err = b.getStockByRef(ctx, txStorage, item.RefType, item.RefID)
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
		rowsAffected, err = txStorage.Querier().AdjustInventory(ctx, inventorydb.AdjustInventoryParams{
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
			serials, err = txStorage.Querier().GetAvailableSerials(ctx, inventorydb.GetAvailableSerialsParams{
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

			if err = txStorage.Querier().UpdateSerialStatus(ctx, inventorydb.UpdateSerialStatusParams{
				ID:     serialIDs,
				Status: inventorydb.InventoryStatusTaken,
			}); err != nil {
				return nil, fmt.Errorf("db update serial status: %w", err)
			}

			result.SerialIDs = serialIDs
		}

		results = append(results, result)
	}

	if err = txStorage.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}

	return results, nil
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

	return b.storage.Querier().UpdateSerialStatus(ctx, inventorydb.UpdateSerialStatusParams{
		ID:     params.SerialIDs,
		Status: params.Status,
	})
}

type ListSerialParams struct {
	paginate.Params

	StockID int64 `validate:"required,gt=0"`
}

// ListSerial returns a paginated list of serials for the given stock ID.
func (b *InventoryHandler) ListSerial(
	ctx restate.Context,
	params ListSerialParams,
) (paginate.PaginateResult[inventorydb.InventorySerial], error) {
	var zero paginate.PaginateResult[inventorydb.InventorySerial]
	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate list serial: %w", err)
	}

	rows, err := b.storage.Querier().ListCountSerial(ctx, inventorydb.ListCountSerialParams{
		StockID: []int64{params.StockID},
		Limit:   params.Limit,
		Offset:  params.Offset(),
	})
	if err != nil {
		return zero, fmt.Errorf("db list serial: %w", err)
	}

	var total null.Int64
	if len(rows) > 0 {
		total.SetValid(rows[0].TotalCount)
	}

	serials := lo.Map(rows, func(r inventorydb.ListCountSerialRow, _ int) inventorydb.InventorySerial {
		return r.InventorySerial
	})

	return paginate.PaginateResult[inventorydb.InventorySerial]{
		PageParams: params.Params,
		Total:      total,
		Data:       serials,
	}, nil
}

type ListMostTakenSkuParams struct {
	paginate.Params

	RefType inventorydb.InventoryStockRefType `validate:"required,validateFn=Valid"`
}

// ListMostTakenSku returns the most reserved SKUs ordered by taken count.
func (b *InventoryHandler) ListMostTakenSku(
	ctx restate.Context,
	params ListMostTakenSkuParams,
) ([]inventorydb.InventoryStock, error) {
	if err := validator.Validate(params); err != nil {
		return nil, fmt.Errorf("validate list most taken: %w", err)
	}

	return b.storage.Querier().ListMostTakenSku(ctx, inventorydb.ListMostTakenSkuParams{
		Limit:   params.Limit,
		Offset:  params.Offset(),
		RefType: params.RefType,
	})
}
