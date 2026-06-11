package checkout

import (
	"fmt"

	restate "github.com/restatedev/sdk-go"

	"shopnexus-server/internal/infras/metrics"
	inventorybiz "shopnexus-server/internal/module/inventory/biz"
	inventorydb "shopnexus-server/internal/module/inventory/db/sqlc"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	"shopnexus-server/internal/shared/idempotency"
	"shopnexus-server/internal/shared/saga"

	"github.com/google/uuid"
	"github.com/samber/lo"
)

// reserveInventory removes the cart lines (skipped for BuyNow) and reserves
// stock, registering the paired compensators. It returns the reserved serial
// IDs keyed by sku.
func (h *CheckoutWorkflow) reserveInventory(
	ctx restate.WorkflowContext,
	saga *saga.Saga,
	input CheckoutWorkflowInput,
	priced pricing,
) (map[uuid.UUID][]string, error) {
	skuIDs := lo.Map(input.Items, func(s CheckoutItem, _ int) uuid.UUID { return s.SkuID })
	checkoutItemMap := lo.KeyBy(input.Items, func(s CheckoutItem) uuid.UUID { return s.SkuID })

	if !input.BuyNow {
		restoreAccountIDs := make([]uuid.UUID, len(input.Items))
		restoreSkuIDs := make([]uuid.UUID, len(input.Items))
		restoreQuantities := make([]int64, len(input.Items))
		for i, item := range input.Items {
			restoreAccountIDs[i] = input.Account.ID
			restoreSkuIDs[i] = item.SkuID
			restoreQuantities[i] = item.Quantity
		}
		saga.Defer("restore_cart", func(ctx restate.Context) error {
			return restate.RunVoid(ctx, func(rctx restate.RunContext) error {
				return h.Storage.Querier().RestoreCheckoutItems(rctx, orderdb.RestoreCheckoutItemsParams{
					AccountIds: restoreAccountIDs,
					SkuIds:     restoreSkuIDs,
					Quantities: restoreQuantities,
				})
			})
		})
		if err := restate.RunVoid(ctx, func(rctx restate.RunContext) error {
			_, e := h.Storage.Querier().RemoveCheckoutItem(rctx, orderdb.RemoveCheckoutItemParams{
				AccountID: input.Account.ID,
				SkuID:     skuIDs,
			})
			return e
		}); err != nil {
			return nil, fmt.Errorf("remove cart items: %w", err)
		}
	}

	// reserveKey is shared across forward (Reserve, claims) and compensator
	// (Release, consumes) so a failure or partial commit unwinds without
	// double-incrementing stock.
	reserveKey := restate.UUID(ctx)
	saga.Defer("release_inventory", func(ctx restate.Context) error {
		return h.inventory.Call().ReleaseInventory(ctx, inventorybiz.ReleaseInventoryParams{
			Keys: idempotency.Keys{ConsumeKey: reserveKey},
			Items: lo.Map(input.Items, func(item CheckoutItem, _ int) inventorybiz.ReleaseInventoryItem {
				return inventorybiz.ReleaseInventoryItem{
					RefType: inventorydb.InventoryStockRefTypeProductSku,
					RefID:   item.SkuID,
					Amount:  checkoutItemMap[item.SkuID].Quantity,
				}
			}),
		})
	})

	inventories, err := h.inventory.Call().ReserveInventory(ctx, inventorybiz.ReserveInventoryParams{
		Keys: idempotency.Keys{ClaimKey: reserveKey},
		Items: lo.Map(input.Items, func(item CheckoutItem, _ int) inventorybiz.ReserveInventoryItem {
			var displayName string
			if sku, ok := priced.skuMap[item.SkuID]; ok {
				if spu, ok := priced.spuMap[sku.SpuID]; ok {
					displayName = spu.Name
				}
			}
			return inventorybiz.ReserveInventoryItem{
				RefType:     inventorydb.InventoryStockRefTypeProductSku,
				RefID:       item.SkuID,
				Amount:      checkoutItemMap[item.SkuID].Quantity,
				DisplayName: displayName,
			}
		}),
	})
	if err != nil {
		metrics.CheckoutItemsCreatedTotal.WithLabelValues("failure").Inc()
		return nil, fmt.Errorf("reserve inventory: %w", err)
	}

	return lo.SliceToMap(inventories, func(i inventorybiz.ReserveInventoryResult) (uuid.UUID, []string) {
		return i.RefID, i.SerialIDs
	}), nil
}
