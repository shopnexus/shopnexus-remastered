package sellerorder

import (
	"encoding/json"
	"fmt"
	"time"

	accountbiz "shopnexus-server/internal/module/account/biz"
	accountmodel "shopnexus-server/internal/module/account/model"
	inventorybiz "shopnexus-server/internal/module/inventory/biz"
	inventorydb "shopnexus-server/internal/module/inventory/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
	orderrepo "shopnexus-server/internal/module/order/repo"
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
	// Before confirm (still orphaned order_item) -> Need Lock r.input.ItemIDs prevent concurrent confirms or reject
	// Then after confirm (order_item linked to order) -> Only Lock r.orderID (no need to lock individual items) to prevent concurrent confirms or cancel/close by buyer/seller.
	// TODO: add lock to prevent concurrent confirms or reject on same items.

	if err := validator.Validate(params); err != nil {
		return fmt.Errorf("validate reject items: %w", err)
	}

	sellerID := params.Account.ID

	// decision: fetch and validate items.
	items, err := restate.Run(ctx, func(rctx restate.RunContext) ([]ordermodel.OrderItem, error) {
		dbItemsRes, err := b.Storage.Querier().ListItem(rctx, orderrepo.ListItemParams{
			Id: params.ItemIDs,
		})
		if err != nil {
			return nil, fmt.Errorf("db list items: %w", err)
		}
		dbItems := dbItemsRes.Data
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

	// execution: release inventory for each item. Cross-module Call self-journals.
	releaseItems := lo.Map(items, func(item ordermodel.OrderItem, _ int) inventorybiz.ReleaseInventoryItem {
		return inventorybiz.ReleaseInventoryItem{
			RefType: inventorydb.InventoryStockRefTypeProductSku,
			RefID:   item.SkuID,
			Amount:  item.Quantity,
		}
	})
	if err = b.inventory.Call().ReleaseInventory(ctx, inventorybiz.ReleaseInventoryParams{
		Items: releaseItems,
	}); err != nil {
		return fmt.Errorf("release inventory: %w", err)
	}

	// tail: refund + cancel + notify per buyer. Each buyer settles atomically in
	// its own decision/execution/tail (see rejectForBuyer).
	buyerItems := make(map[uuid.UUID][]ordermodel.OrderItem)
	for _, item := range items {
		buyerItems[item.AccountID] = append(buyerItems[item.AccountID], item)
	}
	for buyerID, buyerItemList := range buyerItems {
		if err = b.rejectForBuyer(ctx, sellerID, buyerID, buyerItemList); err != nil {
			return err
		}
	}

	return nil
}

// rejectForBuyer settles one buyer's share of a seller rejection: refund the
// settled sessions, cancel pending sessions + items, then credit + notify.
// Self-contained decision/execution/tail so each buyer commits atomically.
func (b *SellerHandler) rejectForBuyer(
	ctx restate.Context,
	sellerID, buyerID uuid.UUID,
	buyerItemList []ordermodel.OrderItem,
) error {
	itemIDs := lo.Map(buyerItemList, func(it ordermodel.OrderItem, _ int) int64 { return it.ID })

	// Look up the payment session for every distinct item. We refund only
	// items whose session actually settled to Success — Pending/Failed
	// items had no money flow through the platform.
	sessionIDs := lo.Uniq(
		lo.Map(buyerItemList, func(it ordermodel.OrderItem, _ int) uuid.UUID { return it.PaymentSessionID }),
	)

	// decision: load sessions and resolve each Success session's original
	// charge (positive Success, no reverses_id) for the refund leg's reverses_id.
	type sessionPlan struct {
		SessionByID         map[uuid.UUID]ordermodel.PaymentSession
		OriginalTxBySession map[uuid.UUID]uuid.UUID
	}
	sp, err := restate.Run(ctx, func(rctx restate.RunContext) (sessionPlan, error) {
		res, e := b.Storage.Querier().ListPaymentSession(rctx, orderrepo.ListPaymentSessionParams{Id: sessionIDs})
		if e != nil {
			return sessionPlan{}, fmt.Errorf("db fetch payment sessions: %w", e)
		}
		byID := lo.KeyBy(res.Data, func(s ordermodel.PaymentSession) uuid.UUID { return s.ID })
		origByID := make(map[uuid.UUID]uuid.UUID)
		for sid, s := range byID {
			if s.Status != ordermodel.StatusSuccess {
				continue
			}
			sessionTxs, te := b.Storage.Querier().ListTransactionsBySession(rctx, sid)
			if te != nil {
				return sessionPlan{}, fmt.Errorf("db list session txs: %w", te)
			}
			if originalTx, ok := ordermodel.FindOriginalCharge(sessionTxs); ok {
				origByID[sid] = originalTx.ID
			}
		}
		return sessionPlan{SessionByID: byID, OriginalTxBySession: origByID}, nil
	})
	if err != nil {
		return err
	}
	sessionByID := sp.SessionByID
	originalTxBySession := sp.OriginalTxBySession

	type itemRefundPlan struct {
		Item       ordermodel.OrderItem
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
		case ordermodel.StatusSuccess:
			if origID, hasOrig := originalTxBySession[s.ID]; hasOrig {
				refundPlans = append(refundPlans, itemRefundPlan{Item: item, OriginalID: origID})
				totalRefund += item.TotalAmount
			}
		case ordermodel.StatusPending:
			pendingSessionIDs = append(pendingSessionIDs, s.ID)
		}
	}
	pendingSessionIDs = lo.Uniq(pendingSessionIDs)

	// Infer buyer currency before the durable Run (cross-module query).
	buyerCurrency, err := b.InferCurrency(ctx, buyerID)
	if err != nil {
		return fmt.Errorf("infer buyer currency: %w", err)
	}

	// Create per-session refund txs and cancel each item atomically.
	// deterministic key: retries must reuse it so the idempotency ledger dedupes
	preMintedRefundTxIDs := make([]uuid.UUID, len(refundPlans))
	for i := range refundPlans {
		preMintedRefundTxIDs[i] = uuid.NewSHA1(uuid.NameSpaceOID, fmt.Appendf(nil, "seller-reject-refund:item:%d", refundPlans[i].Item.ID))
	}

	// execution: per-session refund legs + cancel pending sessions + cancel items.
	refundTxIDs, err := restate.Run(ctx, func(rctx restate.RunContext) ([]uuid.UUID, error) {
		var txIDs []uuid.UUID
		// One refund leg per item, in its own session, reversing the original tx.
		for i, plan := range refundPlans {
			if plan.Item.TotalAmount <= 0 {
				continue
			}
			txID, txErr := b.Storage.Querier().CreateTransaction(rctx, orderrepo.CreateTransactionParams{
				ID:          preMintedRefundTxIDs[i],
				SessionID:   plan.Item.PaymentSessionID,
				Status:      ordermodel.StatusSuccess,
				Note:        "seller reject pre-confirm",
				Data:        json.RawMessage("{}"),
				Amount:      -plan.Item.TotalAmount,
				Currency:    buyerCurrency,
				ReversesID:  uuid.NullUUID{UUID: plan.OriginalID, Valid: true},
				DateSettled: null.TimeFrom(time.Now()),
			})
			if txErr != nil {
				return nil, fmt.Errorf("db create refund tx: %w", txErr)
			}
			txIDs = append(txIDs, txID)
		}

		// Mark any Pending sessions as Cancelled so their timeout / webhook no-ops.
		for _, sid := range pendingSessionIDs {
			if _, err := b.Storage.Querier().MarkPaymentSessionCancelled(rctx, sid); err != nil {
				return nil, fmt.Errorf("db cancel pending session: %w", err)
			}
		}

		// Cancel each item with seller as cancelled_by_id.
		for _, id := range itemIDs {
			if _, err := b.Storage.Querier().CancelItem(rctx, orderrepo.CancelItemParams{
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

	// tail: credit buyer wallet per session — CreditFromSession sums settled
	// positive txs in each session and skips no-ops, so unsettled sessions
	// don't mint balance. Self-journals (cross-module wallet credit).
	_ = totalRefund // kept above only for the empty-list short-circuit clarity
	if len(refundTxIDs) > 0 {
		for _, plan := range refundPlans {
			if _, err := b.refund.CreditFromSession(ctx, ordermodel.CreditFromSessionParams{
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
	rejectedNames := lo.Map(buyerItemList, func(it ordermodel.OrderItem, _ int) string { return it.SkuName })
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

	return nil
}
