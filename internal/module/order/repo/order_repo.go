package orderrepo

import (
	"context"
	"time"

	"github.com/google/uuid"
	null "github.com/guregu/null/v6"
	ordermodel "shopnexus-server/internal/module/order/model"
)

const createOrderIdempotent = `INSERT INTO "order"."order" ("id", "buyer_id", "seller_id", "transport_id", "address", "date_created", "confirmed_by_id", "confirm_session_id", "note")
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT ("id") DO NOTHING`

type CreateOrderIdempotentParams struct {
	ID               uuid.UUID   `json:"id"`
	BuyerID          uuid.UUID   `json:"buyer_id"`
	SellerID         uuid.UUID   `json:"seller_id"`
	TransportID      int64       `json:"transport_id"`
	Address          string      `json:"address"`
	DateCreated      time.Time   `json:"date_created"`
	ConfirmedByID    uuid.UUID   `json:"confirmed_by_id"`
	ConfirmSessionID uuid.UUID   `json:"confirm_session_id"`
	Note             null.String `json:"note"`
}

// CreateOrderIdempotent inserts the order row with a caller-chosen ID (the
// fulfillment workflow key). ON CONFLICT DO NOTHING makes the durable-Run
// retry after a crash-after-commit a no-op instead of a duplicate-key wedge.
func (r *Repository) CreateOrderIdempotent(ctx context.Context, arg CreateOrderIdempotentParams) error {
	_, err := r.db.Exec(ctx, createOrderIdempotent,
		arg.ID,
		arg.BuyerID,
		arg.SellerID,
		arg.TransportID,
		arg.Address,
		arg.DateCreated,
		arg.ConfirmedByID,
		arg.ConfirmSessionID,
		arg.Note,
	)
	return err
}

const getOrderByTransportID = `SELECT o.id, o.buyer_id, o.seller_id, o.transport_id, o.address, o.date_created, o.confirmed_by_id, o.confirm_session_id, o.note FROM "order"."order" o
WHERE o.transport_id = $1`

func (r *Repository) GetOrderByTransportID(ctx context.Context, transportID int64) (ordermodel.Order, error) {
	row := r.db.QueryRow(ctx, getOrderByTransportID, transportID)
	var i ordermodel.Order
	err := row.Scan(
		&i.ID,
		&i.BuyerID,
		&i.SellerID,
		&i.TransportID,
		&i.Address,
		&i.DateCreated,
		&i.ConfirmedByID,
		&i.ConfirmSessionID,
		&i.Note,
	)
	return i, err
}

const hasPurchasedSku = `SELECT EXISTS(
    SELECT 1 FROM "order".item i
    WHERE i.account_id = $1
      AND i.order_id IS NOT NULL
      AND i.date_cancelled IS NULL
      AND i.sku_id = ANY($2::UUID[])
) AS has_purchased`

type HasPurchasedSkuParams struct {
	AccountID uuid.UUID   `json:"account_id"`
	SkuIds    []uuid.UUID `json:"sku_ids"`
}

func (r *Repository) HasPurchasedSku(ctx context.Context, arg HasPurchasedSkuParams) (bool, error) {
	row := r.db.QueryRow(ctx, hasPurchasedSku, arg.AccountID, arg.SkuIds)
	var hasPurchased bool
	err := row.Scan(&hasPurchased)
	return hasPurchased, err
}

const listCountSellerOrder = `SELECT embed_order.id, embed_order.buyer_id, embed_order.seller_id, embed_order.transport_id, embed_order.address, embed_order.date_created, embed_order.confirmed_by_id, embed_order.confirm_session_id, embed_order.note, COUNT(*) OVER() as total_count
FROM "order"."order" embed_order
WHERE embed_order."seller_id" = $1
    AND (embed_order."id"::text ILIKE '%' || $2::text || '%' OR $2 IS NULL)
ORDER BY embed_order."date_created" DESC
LIMIT $4::int
OFFSET $3::int`

type ListCountSellerOrderParams struct {
	SellerID uuid.UUID   `json:"seller_id"`
	Search   null.String `json:"search"`
	Offset   null.Int32  `json:"offset"`
	Limit    null.Int32  `json:"limit"`
}

func (r *Repository) ListCountSellerOrder(ctx context.Context, arg ListCountSellerOrderParams) ([]ordermodel.WithTotal[ordermodel.Order], error) {
	rows, err := r.db.Query(ctx, listCountSellerOrder,
		arg.SellerID,
		arg.Search,
		arg.Offset,
		arg.Limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ordermodel.WithTotal[ordermodel.Order]
	for rows.Next() {
		var w ordermodel.WithTotal[ordermodel.Order]
		if err := rows.Scan(
			&w.Row.ID,
			&w.Row.BuyerID,
			&w.Row.SellerID,
			&w.Row.TransportID,
			&w.Row.Address,
			&w.Row.DateCreated,
			&w.Row.ConfirmedByID,
			&w.Row.ConfirmSessionID,
			&w.Row.Note,
			&w.TotalCount,
		); err != nil {
			return nil, err
		}
		items = append(items, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const listSellerOrders = `SELECT id, buyer_id, seller_id, transport_id, address, date_created, confirmed_by_id, confirm_session_id, note FROM "order"."order"
WHERE "seller_id" = $1
ORDER BY "date_created" DESC
LIMIT $3::INTEGER OFFSET $2::INTEGER`

type ListSellerOrdersParams struct {
	SellerID    uuid.UUID `json:"seller_id"`
	OffsetCount int32     `json:"offset_count"`
	LimitCount  int32     `json:"limit_count"`
}

func (r *Repository) ListSellerOrders(ctx context.Context, arg ListSellerOrdersParams) ([]ordermodel.Order, error) {
	rows, err := r.db.Query(ctx, listSellerOrders, arg.SellerID, arg.OffsetCount, arg.LimitCount)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ordermodel.Order
	for rows.Next() {
		var i ordermodel.Order
		if err := rows.Scan(
			&i.ID,
			&i.BuyerID,
			&i.SellerID,
			&i.TransportID,
			&i.Address,
			&i.DateCreated,
			&i.ConfirmedByID,
			&i.ConfirmSessionID,
			&i.Note,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const listSuccessOrdersBySkus = `SELECT DISTINCT o.id, o.buyer_id, o.seller_id, o.transport_id, o.address, o.date_created, o.confirmed_by_id, o.confirm_session_id, o.note FROM "order"."order" o
JOIN "order".item i ON i.order_id = o.id
WHERE o.buyer_id = $1
  AND i.sku_id = ANY($2::UUID[])
  AND i.date_cancelled IS NULL
ORDER BY o.date_created DESC`

type ListSuccessOrdersBySkusParams struct {
	BuyerID uuid.UUID   `json:"buyer_id"`
	SkuIds  []uuid.UUID `json:"sku_ids"`
}

func (r *Repository) ListSuccessOrdersBySkus(ctx context.Context, arg ListSuccessOrdersBySkusParams) ([]ordermodel.Order, error) {
	rows, err := r.db.Query(ctx, listSuccessOrdersBySkus, arg.BuyerID, arg.SkuIds)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ordermodel.Order
	for rows.Next() {
		var i ordermodel.Order
		if err := rows.Scan(
			&i.ID,
			&i.BuyerID,
			&i.SellerID,
			&i.TransportID,
			&i.Address,
			&i.DateCreated,
			&i.ConfirmedByID,
			&i.ConfirmSessionID,
			&i.Note,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const validateOrderForReview = `SELECT EXISTS(
    SELECT 1 FROM "order"."order" o
    JOIN "order".item i ON i.order_id = o.id
    WHERE o.id = $1
      AND o.buyer_id = $2
      AND i.sku_id = ANY($3::UUID[])
      AND i.date_cancelled IS NULL
) AS is_valid`

type ValidateOrderForReviewParams struct {
	OrderID uuid.UUID   `json:"order_id"`
	BuyerID uuid.UUID   `json:"buyer_id"`
	SkuIds  []uuid.UUID `json:"sku_ids"`
}

func (r *Repository) ValidateOrderForReview(ctx context.Context, arg ValidateOrderForReviewParams) (bool, error) {
	row := r.db.QueryRow(ctx, validateOrderForReview, arg.OrderID, arg.BuyerID, arg.SkuIds)
	var isValid bool
	err := row.Scan(&isValid)
	return isValid, err
}

const listBuyerPendingOrders = `SELECT embed_order.id, embed_order.buyer_id, embed_order.seller_id, embed_order.transport_id, embed_order.address, embed_order.date_created, embed_order.confirmed_by_id, embed_order.confirm_session_id, embed_order.note, COUNT(*) OVER() AS total_count
FROM "order"."order" embed_order
LEFT JOIN "order"."payment_session" ps_confirm
       ON ps_confirm."id" = embed_order."confirm_session_id"
LEFT JOIN "order"."payment_session" ps_payout
       ON ps_payout."id" = embed_order."id" AND ps_payout."kind" = 'seller-payout'
LEFT JOIN "order"."transport" t ON t."id" = embed_order."transport_id"
WHERE embed_order."buyer_id" = $1
  AND NOT "order".is_cancelled(ps_confirm."status", t."status", ps_payout."status")
  AND ps_payout."status" <> 'Success'
  AND t."status" IS DISTINCT FROM 'Success'
ORDER BY embed_order."date_created" DESC
LIMIT $3::int
OFFSET $2::int`

type ListBuyerPendingOrdersParams struct {
	BuyerID uuid.UUID  `json:"buyer_id"`
	Offset  null.Int32 `json:"offset"`
	Limit   null.Int32 `json:"limit"`
}

// ListBuyerPendingOrders returns paginated pending orders for a buyer.
// Buyer-side order list queries partition orders into Cancelled > Completed > Pending
// mutual-exclusion buckets via order.is_cancelled().
func (r *Repository) ListBuyerPendingOrders(ctx context.Context, arg ListBuyerPendingOrdersParams) ([]ordermodel.WithTotal[ordermodel.Order], error) {
	rows, err := r.db.Query(ctx, listBuyerPendingOrders, arg.BuyerID, arg.Offset, arg.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ordermodel.WithTotal[ordermodel.Order]
	for rows.Next() {
		var w ordermodel.WithTotal[ordermodel.Order]
		if err := rows.Scan(
			&w.Row.ID,
			&w.Row.BuyerID,
			&w.Row.SellerID,
			&w.Row.TransportID,
			&w.Row.Address,
			&w.Row.DateCreated,
			&w.Row.ConfirmedByID,
			&w.Row.ConfirmSessionID,
			&w.Row.Note,
			&w.TotalCount,
		); err != nil {
			return nil, err
		}
		items = append(items, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const listBuyerCompletedOrders = `SELECT embed_order.id, embed_order.buyer_id, embed_order.seller_id, embed_order.transport_id, embed_order.address, embed_order.date_created, embed_order.confirmed_by_id, embed_order.confirm_session_id, embed_order.note, COUNT(*) OVER() AS total_count
FROM "order"."order" embed_order
LEFT JOIN "order"."payment_session" ps_confirm
       ON ps_confirm."id" = embed_order."confirm_session_id"
LEFT JOIN "order"."payment_session" ps_payout
       ON ps_payout."id" = embed_order."id" AND ps_payout."kind" = 'seller-payout'
LEFT JOIN "order"."transport" t ON t."id" = embed_order."transport_id"
WHERE embed_order."buyer_id" = $1
  AND NOT "order".is_cancelled(ps_confirm."status", t."status", ps_payout."status")
ORDER BY embed_order."date_created" DESC
LIMIT $3::int
OFFSET $2::int`

type ListBuyerCompletedOrdersParams struct {
	BuyerID uuid.UUID  `json:"buyer_id"`
	Offset  null.Int32 `json:"offset"`
	Limit   null.Int32 `json:"limit"`
}

func (r *Repository) ListBuyerCompletedOrders(ctx context.Context, arg ListBuyerCompletedOrdersParams) ([]ordermodel.WithTotal[ordermodel.Order], error) {
	rows, err := r.db.Query(ctx, listBuyerCompletedOrders, arg.BuyerID, arg.Offset, arg.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ordermodel.WithTotal[ordermodel.Order]
	for rows.Next() {
		var w ordermodel.WithTotal[ordermodel.Order]
		if err := rows.Scan(
			&w.Row.ID,
			&w.Row.BuyerID,
			&w.Row.SellerID,
			&w.Row.TransportID,
			&w.Row.Address,
			&w.Row.DateCreated,
			&w.Row.ConfirmedByID,
			&w.Row.ConfirmSessionID,
			&w.Row.Note,
			&w.TotalCount,
		); err != nil {
			return nil, err
		}
		items = append(items, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const listBuyerCancelledOrders = `SELECT embed_order.id, embed_order.buyer_id, embed_order.seller_id, embed_order.transport_id, embed_order.address, embed_order.date_created, embed_order.confirmed_by_id, embed_order.confirm_session_id, embed_order.note, COUNT(*) OVER() AS total_count
FROM "order"."order" embed_order
LEFT JOIN "order"."payment_session" ps_confirm
       ON ps_confirm."id" = embed_order."confirm_session_id"
LEFT JOIN "order"."payment_session" ps_payout
       ON ps_payout."id" = embed_order."id" AND ps_payout."kind" = 'seller-payout'
LEFT JOIN "order"."transport" t ON t."id" = embed_order."transport_id"
WHERE embed_order."buyer_id" = $1
  AND "order".is_cancelled(ps_confirm."status", t."status", ps_payout."status")
ORDER BY embed_order."date_created" DESC
LIMIT $3::int
OFFSET $2::int`

type ListBuyerCancelledOrdersParams struct {
	BuyerID uuid.UUID  `json:"buyer_id"`
	Offset  null.Int32 `json:"offset"`
	Limit   null.Int32 `json:"limit"`
}

func (r *Repository) ListBuyerCancelledOrders(ctx context.Context, arg ListBuyerCancelledOrdersParams) ([]ordermodel.WithTotal[ordermodel.Order], error) {
	rows, err := r.db.Query(ctx, listBuyerCancelledOrders, arg.BuyerID, arg.Offset, arg.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ordermodel.WithTotal[ordermodel.Order]
	for rows.Next() {
		var w ordermodel.WithTotal[ordermodel.Order]
		if err := rows.Scan(
			&w.Row.ID,
			&w.Row.BuyerID,
			&w.Row.SellerID,
			&w.Row.TransportID,
			&w.Row.Address,
			&w.Row.DateCreated,
			&w.Row.ConfirmedByID,
			&w.Row.ConfirmSessionID,
			&w.Row.Note,
			&w.TotalCount,
		); err != nil {
			return nil, err
		}
		items = append(items, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
