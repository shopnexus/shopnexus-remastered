package orderrepo

import (
	"context"

	"github.com/google/uuid"
	ordermodel "shopnexus-server/internal/module/order/model"
)

const updateCart = `WITH updated AS (
    UPDATE "order"."cart_item"
    SET quantity = $3
    WHERE account_id = $1 AND sku_id = $2
    RETURNING 1
)
INSERT INTO "order"."cart_item" (account_id, sku_id, quantity)
SELECT $1, $2, $3
WHERE NOT EXISTS (SELECT 1 FROM updated)`

// UpdateCartParams holds the upsert args for UpdateCart.
type UpdateCartParams struct {
	AccountID uuid.UUID `json:"account_id"`
	SkuID     uuid.UUID `json:"sku_id"`
	Quantity  int64     `json:"quantity"`
}

func (r *Repository) UpdateCart(ctx context.Context, arg UpdateCartParams) error {
	_, err := r.db.Exec(ctx, updateCart, arg.AccountID, arg.SkuID, arg.Quantity)
	return err
}

const removeCheckoutItem = `DELETE FROM "order"."cart_item"
WHERE account_id = $1
AND sku_id = ANY($2)
RETURNING id, account_id, sku_id, quantity`

// RemoveCheckoutItemParams holds the args for RemoveCheckoutItem.
type RemoveCheckoutItemParams struct {
	AccountID uuid.UUID   `json:"account_id"`
	SkuID     []uuid.UUID `json:"sku_id"`
}

func (r *Repository) RemoveCheckoutItem(ctx context.Context, arg RemoveCheckoutItemParams) ([]ordermodel.CartItem, error) {
	rows, err := r.db.Query(ctx, removeCheckoutItem, arg.AccountID, arg.SkuID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ordermodel.CartItem{}
	for rows.Next() {
		var i ordermodel.CartItem
		if err = rows.Scan(&i.ID, &i.AccountID, &i.SkuID, &i.Quantity); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const restoreCheckoutItems = `INSERT INTO "order"."cart_item" (account_id, sku_id, quantity)
SELECT
    UNNEST($1::uuid[]),
    UNNEST($2::uuid[]),
    UNNEST($3::bigint[])
ON CONFLICT (account_id, sku_id) DO UPDATE
    SET quantity = EXCLUDED.quantity`

// RestoreCheckoutItemsParams holds the arrays for RestoreCheckoutItems.
type RestoreCheckoutItemsParams struct {
	AccountIds []uuid.UUID `json:"account_ids"`
	SkuIds     []uuid.UUID `json:"sku_ids"`
	Quantities []int64     `json:"quantities"`
}

func (r *Repository) RestoreCheckoutItems(ctx context.Context, arg RestoreCheckoutItemsParams) error {
	_, err := r.db.Exec(ctx, restoreCheckoutItems, arg.AccountIds, arg.SkuIds, arg.Quantities)
	return err
}
