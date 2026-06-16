package checkout

import (
	"fmt"

	restate "github.com/restatedev/sdk-go"

	"shopnexus-server/internal/infras/metrics"
	inventorybiz "shopnexus-server/internal/module/inventory/biz"
	inventorydb "shopnexus-server/internal/module/inventory/db/sqlc"
	orderrepo "shopnexus-server/internal/module/order/repo"
	"shopnexus-server/internal/shared/idempotency"

	"github.com/google/uuid"
	"github.com/samber/lo"
)

func (r *checkoutRun) reserve() error {
	ctx, input := r.ctx, r.input
	skuIDs := lo.Map(input.Items, func(s CheckoutItem, _ int) uuid.UUID { return s.SkuID })
	qtyBySku := lo.KeyBy(input.Items, func(s CheckoutItem) uuid.UUID { return s.SkuID })

	// Step 1: remove cart items (+ restore compensator) — skipped for buy-now
	if !input.BuyNow {
		accountIDs := make([]uuid.UUID, len(input.Items))
		restoreSkuIDs := make([]uuid.UUID, len(input.Items))
		quantities := make([]int64, len(input.Items))
		for i, item := range input.Items {
			accountIDs[i] = input.Account.ID
			restoreSkuIDs[i] = item.SkuID
			quantities[i] = item.Quantity
		}
		r.saga.Defer("restore_cart", func(ctx restate.Context) error {
			return restate.RunVoid(ctx, func(rctx restate.RunContext) error {
				return r.Storage.Querier().RestoreCheckoutItems(rctx, orderrepo.RestoreCheckoutItemsParams{
					AccountIds: accountIDs,
					SkuIds:     restoreSkuIDs,
					Quantities: quantities,
				})
			})
		})
		if err := restate.RunVoid(ctx, func(rctx restate.RunContext) error {
			_, e := r.Storage.Querier().RemoveCheckoutItem(rctx, orderrepo.RemoveCheckoutItemParams{
				AccountID: input.Account.ID,
				SkuID:     skuIDs,
			})
			return e
		}); err != nil {
			return fmt.Errorf("remove cart items: %w", err)
		}
	}

	// Step 2: reserve inventory (+ release compensator)
	// reserveKey pairs forward (Reserve, claims) with the compensator (Release,
	// consumes) so a failure or partial commit unwinds without double-counting stock.
	reserveKey := restate.UUID(ctx)
	r.saga.Defer("release_inventory", func(ctx restate.Context) error {
		return r.inventory.Call().ReleaseInventory(ctx, inventorybiz.ReleaseInventoryParams{
			Keys: idempotency.Keys{ConsumeKey: reserveKey},
			Items: lo.Map(input.Items, func(item CheckoutItem, _ int) inventorybiz.ReleaseInventoryItem {
				return inventorybiz.ReleaseInventoryItem{
					RefType: inventorydb.InventoryStockRefTypeProductSku,
					RefID:   item.SkuID,
					Amount:  qtyBySku[item.SkuID].Quantity,
				}
			}),
		})
	})

	inventories, err := r.inventory.Call().ReserveInventory(ctx, inventorybiz.ReserveInventoryParams{
		Keys: idempotency.Keys{ClaimKey: reserveKey},
		Items: lo.Map(input.Items, func(item CheckoutItem, _ int) inventorybiz.ReserveInventoryItem {
			return inventorybiz.ReserveInventoryItem{
				RefType:     inventorydb.InventoryStockRefTypeProductSku,
				RefID:       item.SkuID,
				Amount:      qtyBySku[item.SkuID].Quantity,
				DisplayName: r.spuMap[r.skuMap[item.SkuID].SpuID].Name,
			}
		}),
	})
	if err != nil {
		metrics.CheckoutItemsCreatedTotal.WithLabelValues("failure").Inc()
		return fmt.Errorf("reserve inventory: %w", err)
	}

	r.serialIDs = lo.SliceToMap(inventories, func(i inventorybiz.ReserveInventoryResult) (uuid.UUID, []string) {
		return i.RefID, i.SerialIDs
	})
	return nil
}
