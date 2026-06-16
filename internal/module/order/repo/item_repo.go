package orderrepo

import (
	"context"

	"github.com/google/uuid"
	ordermodel "shopnexus-server/internal/module/order/model"
)

const cancelItem = `UPDATE "order"."item"
SET "date_cancelled" = CURRENT_TIMESTAMP,
    "cancelled_by_id" = $1
WHERE "id" = $2 AND "date_cancelled" IS NULL
RETURNING id, order_id, account_id, seller_id, sku_id, spu_id, sku_name, address, note, serial_ids, quantity, transport_option, subtotal_amount, total_amount, source_currency, payment_session_id, date_cancelled, cancelled_by_id, date_created`

type CancelItemParams struct {
	CancelledByID uuid.NullUUID `json:"cancelled_by_id"`
	ID            int64         `json:"id"`
}

// CancelItem marks an item cancelled. The compensating refund transaction is inserted
// separately by biz code.
func (r *Repository) CancelItem(ctx context.Context, arg CancelItemParams) (ordermodel.OrderItem, error) {
	row := r.db.QueryRow(ctx, cancelItem, arg.CancelledByID, arg.ID)
	var i ordermodel.OrderItem
	err := row.Scan(
		&i.ID,
		&i.OrderID,
		&i.AccountID,
		&i.SellerID,
		&i.SkuID,
		&i.SpuID,
		&i.SkuName,
		&i.Address,
		&i.Note,
		&i.SerialIds,
		&i.Quantity,
		&i.TransportOption,
		&i.SubtotalAmount,
		&i.TotalAmount,
		&i.SourceCurrency,
		&i.PaymentSessionID,
		&i.DateCancelled,
		&i.CancelledByID,
		&i.DateCreated,
	)
	return i, err
}

const cancelItemsByIDs = `UPDATE "order"."item"
SET "date_cancelled" = CURRENT_TIMESTAMP,
    "cancelled_by_id" = $1
WHERE "id" = ANY($2::BIGINT[])
  AND "order_id" IS NULL
  AND "date_cancelled" IS NULL`

type CancelItemsByIDsParams struct {
	CancelledByID uuid.NullUUID `json:"cancelled_by_id"`
	ItemIds       []int64       `json:"item_ids"`
}

func (r *Repository) CancelItemsByIDs(ctx context.Context, arg CancelItemsByIDsParams) (int64, error) {
	result, err := r.db.Exec(ctx, cancelItemsByIDs, arg.CancelledByID, arg.ItemIds)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

const countBuyerCancelledItems = `SELECT COUNT(*) FROM "order"."item" i
JOIN "order"."payment_session" ps ON ps."id" = i."payment_session_id"
WHERE i."account_id" = $1
  AND i."order_id" IS NULL
  AND (ps."status" IN ('Failed', 'Cancelled') OR i."date_cancelled" IS NOT NULL)`

func (r *Repository) CountBuyerCancelledItems(ctx context.Context, accountID uuid.UUID) (int64, error) {
	row := r.db.QueryRow(ctx, countBuyerCancelledItems, accountID)
	var count int64
	err := row.Scan(&count)
	return count, err
}

const countBuyerPendingItems = `SELECT COUNT(*) FROM "order"."item" i
JOIN "order"."payment_session" ps ON ps."id" = i."payment_session_id"
WHERE i."account_id" = $1
  AND i."order_id" IS NULL
  AND i."date_cancelled" IS NULL
  AND ps."status" IN ('Pending', 'Success')`

func (r *Repository) CountBuyerPendingItems(ctx context.Context, accountID uuid.UUID) (int64, error) {
	row := r.db.QueryRow(ctx, countBuyerPendingItems, accountID)
	var count int64
	err := row.Scan(&count)
	return count, err
}

const countSellerPendingItems = `SELECT COUNT(*) FROM "order"."item" i
JOIN "order"."payment_session" ps ON ps."id" = i."payment_session_id"
WHERE i."seller_id" = $1
  AND i."order_id" IS NULL
  AND i."date_cancelled" IS NULL
  AND ps."status" = 'Success'`

func (r *Repository) CountSellerPendingItems(ctx context.Context, sellerID uuid.UUID) (int64, error) {
	row := r.db.QueryRow(ctx, countSellerPendingItems, sellerID)
	var count int64
	err := row.Scan(&count)
	return count, err
}

const listBuyerCancelledItems = `SELECT i.id, i.order_id, i.account_id, i.seller_id, i.sku_id, i.spu_id, i.sku_name, i.address, i.note, i.serial_ids, i.quantity, i.transport_option, i.subtotal_amount, i.total_amount, i.source_currency, i.payment_session_id, i.date_cancelled, i.cancelled_by_id, i.date_created FROM "order"."item" i
JOIN "order"."payment_session" ps ON ps."id" = i."payment_session_id"
WHERE i."account_id" = $1
  AND i."order_id" IS NULL
  AND (ps."status" IN ('Failed', 'Cancelled') OR i."date_cancelled" IS NOT NULL)
ORDER BY i."date_created" DESC`

// ListBuyerCancelledItems returns pre-confirm items the buyer can no longer act on:
// either the checkout failed/was cancelled or the item was individually cancelled.
func (r *Repository) ListBuyerCancelledItems(ctx context.Context, accountID uuid.UUID) ([]ordermodel.OrderItem, error) {
	rows, err := r.db.Query(ctx, listBuyerCancelledItems, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ordermodel.OrderItem
	for rows.Next() {
		var i ordermodel.OrderItem
		if err = rows.Scan(
			&i.ID,
			&i.OrderID,
			&i.AccountID,
			&i.SellerID,
			&i.SkuID,
			&i.SpuID,
			&i.SkuName,
			&i.Address,
			&i.Note,
			&i.SerialIds,
			&i.Quantity,
			&i.TransportOption,
			&i.SubtotalAmount,
			&i.TotalAmount,
			&i.SourceCurrency,
			&i.PaymentSessionID,
			&i.DateCancelled,
			&i.CancelledByID,
			&i.DateCreated,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const listBuyerPendingItems = `SELECT i.id, i.order_id, i.account_id, i.seller_id, i.sku_id, i.spu_id, i.sku_name, i.address, i.note, i.serial_ids, i.quantity, i.transport_option, i.subtotal_amount, i.total_amount, i.source_currency, i.payment_session_id, i.date_cancelled, i.cancelled_by_id, i.date_created FROM "order"."item" i
JOIN "order"."payment_session" ps ON ps."id" = i."payment_session_id"
WHERE i."account_id" = $1
  AND i."order_id" IS NULL
  AND i."date_cancelled" IS NULL
  AND ps."status" IN ('Pending', 'Success')
ORDER BY i."date_created" DESC`

// ListBuyerPendingItems returns pre-confirm items still reachable to the buyer:
// payment session is either in-flight (Pending) or settled (Success, awaiting seller confirm).
func (r *Repository) ListBuyerPendingItems(ctx context.Context, accountID uuid.UUID) ([]ordermodel.OrderItem, error) {
	rows, err := r.db.Query(ctx, listBuyerPendingItems, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ordermodel.OrderItem
	for rows.Next() {
		var i ordermodel.OrderItem
		if err = rows.Scan(
			&i.ID,
			&i.OrderID,
			&i.AccountID,
			&i.SellerID,
			&i.SkuID,
			&i.SpuID,
			&i.SkuName,
			&i.Address,
			&i.Note,
			&i.SerialIds,
			&i.Quantity,
			&i.TransportOption,
			&i.SubtotalAmount,
			&i.TotalAmount,
			&i.SourceCurrency,
			&i.PaymentSessionID,
			&i.DateCancelled,
			&i.CancelledByID,
			&i.DateCreated,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const listItemsByPaymentSession = `SELECT id, order_id, account_id, seller_id, sku_id, spu_id, sku_name, address, note, serial_ids, quantity, transport_option, subtotal_amount, total_amount, source_currency, payment_session_id, date_cancelled, cancelled_by_id, date_created FROM "order"."item" WHERE "payment_session_id" = $1`

func (r *Repository) ListItemsByPaymentSession(ctx context.Context, paymentSessionID uuid.UUID) ([]ordermodel.OrderItem, error) {
	rows, err := r.db.Query(ctx, listItemsByPaymentSession, paymentSessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ordermodel.OrderItem
	for rows.Next() {
		var i ordermodel.OrderItem
		if err = rows.Scan(
			&i.ID,
			&i.OrderID,
			&i.AccountID,
			&i.SellerID,
			&i.SkuID,
			&i.SpuID,
			&i.SkuName,
			&i.Address,
			&i.Note,
			&i.SerialIds,
			&i.Quantity,
			&i.TransportOption,
			&i.SubtotalAmount,
			&i.TotalAmount,
			&i.SourceCurrency,
			&i.PaymentSessionID,
			&i.DateCancelled,
			&i.CancelledByID,
			&i.DateCreated,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const listPendingPaymentItemsByPaymentSession = `SELECT id, order_id, account_id, seller_id, sku_id, spu_id, sku_name, address, note, serial_ids, quantity, transport_option, subtotal_amount, total_amount, source_currency, payment_session_id, date_cancelled, cancelled_by_id, date_created FROM "order"."item"
WHERE "payment_session_id" = $1
  AND "order_id" IS NULL
  AND "date_cancelled" IS NULL`

func (r *Repository) ListPendingPaymentItemsByPaymentSession(ctx context.Context, paymentSessionID uuid.UUID) ([]ordermodel.OrderItem, error) {
	rows, err := r.db.Query(ctx, listPendingPaymentItemsByPaymentSession, paymentSessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ordermodel.OrderItem
	for rows.Next() {
		var i ordermodel.OrderItem
		if err = rows.Scan(
			&i.ID,
			&i.OrderID,
			&i.AccountID,
			&i.SellerID,
			&i.SkuID,
			&i.SpuID,
			&i.SkuName,
			&i.Address,
			&i.Note,
			&i.SerialIds,
			&i.Quantity,
			&i.TransportOption,
			&i.SubtotalAmount,
			&i.TotalAmount,
			&i.SourceCurrency,
			&i.PaymentSessionID,
			&i.DateCancelled,
			&i.CancelledByID,
			&i.DateCreated,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const listSellerPendingItems = `SELECT i.id, i.order_id, i.account_id, i.seller_id, i.sku_id, i.spu_id, i.sku_name, i.address, i.note, i.serial_ids, i.quantity, i.transport_option, i.subtotal_amount, i.total_amount, i.source_currency, i.payment_session_id, i.date_cancelled, i.cancelled_by_id, i.date_created FROM "order"."item" i
JOIN "order"."payment_session" ps ON ps."id" = i."payment_session_id"
WHERE i."seller_id" = $1
  AND i."order_id" IS NULL
  AND i."date_cancelled" IS NULL
  AND ps."status" = 'Success'
ORDER BY i."date_created" DESC`

// ListSellerPendingItems returns only items whose payment session has succeeded —
// sellers should not see items still awaiting buyer payment or whose session has failed.
func (r *Repository) ListSellerPendingItems(ctx context.Context, sellerID uuid.UUID) ([]ordermodel.OrderItem, error) {
	rows, err := r.db.Query(ctx, listSellerPendingItems, sellerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ordermodel.OrderItem
	for rows.Next() {
		var i ordermodel.OrderItem
		if err = rows.Scan(
			&i.ID,
			&i.OrderID,
			&i.AccountID,
			&i.SellerID,
			&i.SkuID,
			&i.SpuID,
			&i.SkuName,
			&i.Address,
			&i.Note,
			&i.SerialIds,
			&i.Quantity,
			&i.TransportOption,
			&i.SubtotalAmount,
			&i.TotalAmount,
			&i.SourceCurrency,
			&i.PaymentSessionID,
			&i.DateCancelled,
			&i.CancelledByID,
			&i.DateCreated,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const setItemsOrderID = `UPDATE "order"."item"
SET "order_id" = $1
WHERE "id" = ANY($2::BIGINT[]) AND "order_id" IS NULL`

type SetItemsOrderIDParams struct {
	OrderID uuid.NullUUID `json:"order_id"`
	ItemIds []int64       `json:"item_ids"`
}

func (r *Repository) SetItemsOrderID(ctx context.Context, arg SetItemsOrderIDParams) error {
	_, err := r.db.Exec(ctx, setItemsOrderID, arg.OrderID, arg.ItemIds)
	return err
}

const sumTotalAmountByOrder = `SELECT COALESCE(SUM("total_amount"), 0)::BIGINT AS total
FROM "order"."item"
WHERE "order_id" = $1 AND "date_cancelled" IS NULL`

func (r *Repository) SumTotalAmountByOrder(ctx context.Context, orderID uuid.NullUUID) (int64, error) {
	row := r.db.QueryRow(ctx, sumTotalAmountByOrder, orderID)
	var total int64
	err := row.Scan(&total)
	return total, err
}

const unlinkItemsFromOrder = `UPDATE "order"."item"
SET "order_id" = NULL
WHERE "order_id" = $1`

func (r *Repository) UnlinkItemsFromOrder(ctx context.Context, orderID uuid.NullUUID) error {
	_, err := r.db.Exec(ctx, unlinkItemsFromOrder, orderID)
	return err
}
