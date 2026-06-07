package sellerorder

import (
	"encoding/json"
	"fmt"
	"time"

	accountbiz "shopnexus-server/internal/module/account/biz"
	accountmodel "shopnexus-server/internal/module/account/model"
	inventorybiz "shopnexus-server/internal/module/inventory/biz"
	inventorydb "shopnexus-server/internal/module/inventory/db/sqlc"
	wfbase "shopnexus-server/internal/module/order/biz/workflow/base"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	restate "github.com/restatedev/sdk-go"
	"github.com/samber/lo"
)

type RejectSellerPendingParams struct {
	Account accountmodel.AuthenticatedAccount
	ItemIDs []int64 `validate:"required,min=1,max=1000"`
}

// RejectSellerPending rejects pending items owned by the seller, releases inventory, and refunds buyers.
func (b *SellerHandler) RejectSellerPending(ctx restate.Context, params RejectSellerPendingParams) error {
	// Lock: exclusive — same key as ConfirmSellerPending.
	unlock := b.locker.Lock(ctx, fmt.Sprintf("order:seller-pending:%s", params.Account.ID))
	defer unlock()

	if err := validator.Validate(params); err != nil {
		return fmt.Errorf("validate reject items: %w", err)
	}

	sellerID := params.Account.ID

	// Fetch and validate items.
	items, err := restate.Run(ctx, func(ctx restate.RunContext) ([]orderdb.OrderItem, error) {
		dbItems, err := b.Storage.Querier().ListItem(ctx, orderdb.ListItemParams{
			ID: params.ItemIDs,
		})
		if err != nil {
			return nil, fmt.Errorf("db list items: %w", err)
		}
		if len(dbItems) != len(params.ItemIDs) {
			return nil, ordermodel.ErrOrderItemNotFound
		}

		for _, item := range dbItems {
			if item.OrderID.Valid {
				return nil, ordermodel.ErrItemAlreadyConfirmed
			}
			if item.DateCancelled.Valid {
				return nil, ordermodel.ErrItemAlreadyCancelled
			}
			if item.SellerID != sellerID {
				return nil, ordermodel.ErrItemNotOwnedBySeller
			}
		}
		return dbItems, nil
	})
	if err != nil {
		return fmt.Errorf("fetch items: %w", err)
	}

	// Release inventory for each item (outside Run — cross-module).
	releaseItems := lo.Map(items, func(item orderdb.OrderItem, _ int) inventorybiz.ReleaseInventoryItem {
		return inventorybiz.ReleaseInventoryItem{
			RefType: inventorydb.InventoryStockRefTypeProductSku,
			RefID:   item.SkuID,
			Amount:  item.Quantity,
		}
	})
	if err := b.inventory.ReleaseInventory(ctx, inventorybiz.ReleaseInventoryParams{
		Items: releaseItems,
	}); err != nil {
		return fmt.Errorf("release inventory: %w", err)
	}

	// Group items by buyer and process refunds per buyer.
	buyerItems := make(map[uuid.UUID][]orderdb.OrderItem)
	for _, item := range items {
		buyerItems[item.AccountID] = append(buyerItems[item.AccountID], item)
	}

	for buyerID, buyerItemList := range buyerItems {
		itemIDs := lo.Map(buyerItemList, func(it orderdb.OrderItem, _ int) int64 { return it.ID })

		// Look up the payment session for every distinct item. We refund only
		// items whose session actually settled to Success — Pending/Failed
		// items had no money flow through the platform.
		sessionIDs := lo.Uniq(
			lo.Map(buyerItemList, func(it orderdb.OrderItem, _ int) uuid.UUID { return it.PaymentSessionID }),
		)
		sessions, err := restate.Run(ctx, func(ctx restate.RunContext) ([]orderdb.OrderPaymentSession, error) {
			return b.Storage.Querier().ListPaymentSession(ctx, orderdb.ListPaymentSessionParams{ID: sessionIDs})
		})
		if err != nil {
			return fmt.Errorf("db fetch payment sessions: %w", err)
		}
		sessionByID := lo.KeyBy(sessions, func(s orderdb.OrderPaymentSession) uuid.UUID { return s.ID })

		// For each Success session, fetch its original tx (positive Success, no reverses_id)
		// to use as reverses_id on the refund leg. Per design: single original per session.
		originalTxBySession := make(map[uuid.UUID]uuid.UUID)
		for sid, s := range sessionByID {
			if s.Status != orderdb.OrderStatusSuccess {
				continue
			}
			sessionTxs, err := restate.Run(ctx, func(ctx restate.RunContext) ([]orderdb.OrderTransaction, error) {
				return b.Storage.Querier().ListTransactionsBySession(ctx, sid)
			})
			if err != nil {
				return fmt.Errorf("db list session txs: %w", err)
			}
			if originalTx, ok := wfbase.FindOriginalCharge(sessionTxs); ok {
				originalTxBySession[sid] = originalTx.ID
			}
		}

		type itemRefundPlan struct {
			Item       orderdb.OrderItem
			OriginalID uuid.UUID
		}
		var refundPlans []itemRefundPlan
		var pendingSessionIDs []uuid.UUID
		var totalRefund int64
		for _, item := range buyerItemList {
			s, ok := sessionByID[item.PaymentSessionID]
			if !ok {
				continue
			}
			switch s.Status {
			case orderdb.OrderStatusSuccess:
				if origID, hasOrig := originalTxBySession[s.ID]; hasOrig {
					refundPlans = append(refundPlans, itemRefundPlan{Item: item, OriginalID: origID})
					totalRefund += item.TotalAmount
				}
			case orderdb.OrderStatusPending:
				pendingSessionIDs = append(pendingSessionIDs, s.ID)
			}
		}
		pendingSessionIDs = lo.Uniq(pendingSessionIDs)

		// Infer buyer currency before the durable Run (outside Run — cross-module).
		buyerCurrency, err := b.InferCurrency(ctx, buyerID)
		if err != nil {
			return fmt.Errorf("infer buyer currency: %w", err)
		}

		// Create per-session refund txs and cancel each item atomically.
		preMintedRefundTxIDs := make([]uuid.UUID, len(refundPlans))
		for i := range refundPlans {
			preMintedRefundTxIDs[i] = restate.UUID(ctx)
		}
		refundTxIDs, err := restate.Run(ctx, func(ctx restate.RunContext) ([]uuid.UUID, error) {
			var txIDs []uuid.UUID
			// One refund leg per item, in its own session, reversing the original tx.
			for i, plan := range refundPlans {
				if plan.Item.TotalAmount <= 0 {
					continue
				}
				tx, txErr := b.Storage.Querier().CreateDefaultTransaction(ctx, orderdb.CreateDefaultTransactionParams{
					ID:            preMintedRefundTxIDs[i],
					SessionID:     plan.Item.PaymentSessionID,
					Status:        orderdb.OrderStatusSuccess,
					Note:          "seller reject pre-confirm",
					Error:         null.String{},
					PaymentOption: null.String{},
					Data:          json.RawMessage("{}"),
					Amount:        -plan.Item.TotalAmount,
					Currency:      buyerCurrency,
					ReversesID:    uuid.NullUUID{UUID: plan.OriginalID, Valid: true},
					DateSettled:   null.TimeFrom(time.Now()),
					DateExpired:   null.Time{},
				})
				if txErr != nil {
					return nil, fmt.Errorf("db create refund tx: %w", txErr)
				}
				txIDs = append(txIDs, tx.ID)
			}

			// Mark any Pending sessions as Cancelled so their timeout / webhook no-ops.
			for _, sid := range pendingSessionIDs {
				if _, err := b.Storage.Querier().MarkPaymentSessionCancelled(ctx, sid); err != nil {
					return nil, fmt.Errorf("db cancel pending session: %w", err)
				}
			}

			// Cancel each item with seller as cancelled_by_id.
			for _, id := range itemIDs {
				if _, err := b.Storage.Querier().CancelItem(ctx, orderdb.CancelItemParams{
					CancelledByID: uuid.NullUUID{UUID: sellerID, Valid: true},
					ID:            id,
				}); err != nil {
					return nil, fmt.Errorf("db cancel item: %w", err)
				}
			}

			return txIDs, nil
		})
		if err != nil {
			return fmt.Errorf("reject items for buyer: %w", err)
		}

		// Credit buyer wallet per session — CreditFromSession sums settled positive
		// txs in each session and skips no-ops, so unsettled sessions don't mint balance.
		_ = totalRefund // kept above only for the empty-list short-circuit clarity
		if len(refundTxIDs) > 0 {
			for _, plan := range refundPlans {
				if _, err := b.CreditFromSession(ctx, wfbase.CreditFromSessionParams{
					SessionID:  plan.Item.PaymentSessionID,
					AccountID:  buyerID,
					CreditType: "Refund",
					Note:       "seller reject pre-confirm refund",
				}); err != nil {
					return fmt.Errorf("credit buyer from session: %w", err)
				}
			}
		}

		// Notify buyer (fire-and-forget).
		rejectedNames := lo.Map(buyerItemList, func(it orderdb.OrderItem, _ int) string { return it.SkuName })
		rejectSummary := ordermodel.SummarizeNames(rejectedNames)
		if err = b.Notify(ctx, accountbiz.CreateNotificationParams{
			AccountID: buyerID,
			Type:      accountmodel.NotiItemsRejected,
			Channel:   accountmodel.ChannelInApp,
			Title:     "Items rejected",
			Content:   fmt.Sprintf("%s has been rejected by the seller.", rejectSummary),
			Metadata:  json.RawMessage(`{}`),
		}); err != nil {
			return fmt.Errorf("notify buyer: %w", err)
		}
	}

	return nil
}
