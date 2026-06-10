package refund

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	accountbiz "shopnexus-server/internal/module/account/biz"
	inventorybiz "shopnexus-server/internal/module/inventory/biz"
	inventorydb "shopnexus-server/internal/module/inventory/db/sqlc"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/samber/lo"
)

// CreditFromSession credits the recipient with the sum of positive-amount Success
// transactions in the given session. Use this when a session is being voided or
// refunded — credits only legs that actually settled, never minting balance for
// unsettled / failed / pending legs. Returns the amount credited; 0 means no-op.
func (b *RefundHandler) CreditFromSession(
	ctx context.Context,
	params ordermodel.CreditFromSessionParams,
) (int64, error) {
	settled, err := func() (int64, error) {
		txs, err := b.Storage.Querier().ListTransactionsBySession(ctx, params.SessionID)
		if err != nil {
			return 0, fmt.Errorf("list session txs: %w", err)
		}
		var total int64
		for _, tx := range txs {
			if tx.Status == orderdb.OrderStatusSuccess && tx.Amount > 0 {
				total += tx.Amount
			}
		}
		return total, nil
	}()
	if err != nil {
		return 0, fmt.Errorf("read session txs: %w", err)
	}
	if settled == 0 {
		return 0, nil
	}

	if err = b.Account().WalletCredit(ctx, accountbiz.WalletCreditParams{
		AccountID: params.AccountID,
		Amount:    settled,
		Type:      params.CreditType,
		Reference: fmt.Sprintf("session:%s %s", params.SessionID, params.Reference),
		Note:      params.Note,
	}); err != nil {
		return 0, fmt.Errorf("wallet credit from session: %w", err)
	}
	return settled, nil
}

// ExecuteRefundCredit performs the actual credit flow: insert refund tx,
// flip refund.status to Accepted, cancel items, credit buyer wallet, restock
// inventory. Used by all 3 paths that end in Accepted (seller approve,
// auto-accept timeout, admin dismiss). Callers outside the fulfillment
// workflow must signal it afterwards (Send().OnRefundChanged) so its escrow
// loop re-snapshots; the in-workflow caller re-snapshots on loop continue.
func (b *RefundHandler) ExecuteRefundCredit(
	ctx context.Context,
	refund orderdb.OrderRefund,
	deciderID uuid.UUID,
	reason ordermodel.RefundCreditReason,
) (orderdb.OrderRefund, error) {
	var zero orderdb.OrderRefund

	res, err := b.Storage.Querier().ListItem(ctx, orderdb.ListItemParams{
		OrderId: []uuid.UUID{refund.OrderID},
	})
	if err != nil {
		return zero, fmt.Errorf("list items: %w", err)
	}
	items := res.Data
	var anyItem orderdb.OrderItem
	var refundAmount int64
	for _, it := range items {
		if !it.DateCancelled.Valid {
			if anyItem.ID == 0 {
				anyItem = it
			}
			refundAmount += it.TotalAmount
		}
	}
	if anyItem.ID == 0 {
		return zero, fmt.Errorf("no non-cancelled items: %w", ordermodel.ErrOrderItemNotFound)
	}

	order, err := b.Storage.Querier().GetOrder(ctx, orderdb.GetOrderParams{
		ID: uuid.NullUUID{UUID: refund.OrderID, Valid: true},
	})
	if err != nil {
		return zero, fmt.Errorf("get order: %w", err)
	}

	buyerCurrency, err := b.InferCurrency(ctx, order.BuyerID)
	if err != nil {
		return zero, fmt.Errorf("infer currency: %w", err)
	}

	sessionTxs, err := b.Storage.Querier().ListTransactionsBySession(ctx, anyItem.PaymentSessionID)
	if err != nil {
		return zero, fmt.Errorf("list session txs: %w", err)
	}
	originalTx, ok := ordermodel.FindOriginalCharge(sessionTxs)
	if !ok {
		return zero, fmt.Errorf("no original tx: %w", ordermodel.ErrOrderItemNotFound)
	}
	originalTxID := uuid.NullUUID{UUID: originalTx.ID, Valid: true}

	// deterministic key: retries must reuse it so the idempotency ledger dedupes
	refundTxID := uuid.NewSHA1(uuid.NameSpaceOID, fmt.Appendf(nil, "refund-credit:refund:%s", refund.ID))
	updated, err := func() (orderdb.OrderRefund, error) {
		refundTx, e := b.Storage.Querier().CreateDefaultTransaction(ctx, orderdb.CreateDefaultTransactionParams{
			ID:            refundTxID,
			SessionID:     anyItem.PaymentSessionID,
			Status:        orderdb.OrderStatusSuccess,
			Note:          fmt.Sprintf("refund %s: %s", refund.ID, reason),
			Error:         null.String{},
			PaymentOption: null.String{},
			Data:          json.RawMessage("{}"),
			Amount:        -refundAmount,
			Currency:      buyerCurrency,
			ReversesID:    originalTxID,
			DateSettled:   null.TimeFrom(time.Now()),
			DateExpired:   null.Time{},
		})
		if e != nil {
			return orderdb.OrderRefund{}, fmt.Errorf("create refund tx: %w", e)
		}

		// Pick the right SQL based on the source state.
		var u orderdb.OrderRefund
		switch refund.Status {
		case orderdb.OrderRefundStatusAwaitingSellerReview:
			u, e = b.Storage.Querier().SellerApproveRefund(ctx, orderdb.SellerApproveRefundParams{
				ID:         refund.ID,
				RefundTxID: uuid.NullUUID{UUID: refundTx.ID, Valid: true},
			})
		case orderdb.OrderRefundStatusDisputed:
			u, e = b.Storage.Querier().AdminDismissDispute(ctx, orderdb.AdminDismissDisputeParams{
				ID:         refund.ID,
				RefundTxID: uuid.NullUUID{UUID: refundTx.ID, Valid: true},
			})
		default:
			return orderdb.OrderRefund{}, ordermodel.ErrRefundWrongStage
		}
		if e != nil {
			return orderdb.OrderRefund{}, fmt.Errorf("approve refund: %w", e)
		}

		for _, it := range items {
			if it.DateCancelled.Valid {
				continue
			}
			if _, ce := b.Storage.Querier().CancelItem(ctx, orderdb.CancelItemParams{
				ID:            it.ID,
				CancelledByID: uuid.NullUUID{UUID: deciderID, Valid: true},
			}); ce != nil {
				return orderdb.OrderRefund{}, fmt.Errorf("cancel item: %w", ce)
			}
		}
		return u, nil
	}()
	if err != nil {
		return zero, fmt.Errorf("execute refund ops: %w", err)
	}

	if _, err := b.CreditFromSession(ctx, ordermodel.CreditFromSessionParams{
		SessionID:  anyItem.PaymentSessionID,
		AccountID:  order.BuyerID,
		CreditType: "Refund",
		Reference:  fmt.Sprintf("refund:%s", refund.ID),
		Note:       fmt.Sprintf("refund accepted (%s)", reason),
	}); err != nil {
		return zero, fmt.Errorf("wallet credit: %w", err)
	}

	// Restock inventory for every item we just cancelled. Mirrors the release
	// path in CancelBuyerPending / RejectSellerPending so the SKU quantity goes
	// back up when the refund settles. Cross-module call lives outside the
	// durable Run because the inventory module owns its own idempotency.
	releaseItems := lo.FilterMap(items, func(it orderdb.OrderItem, _ int) (inventorybiz.ReleaseInventoryItem, bool) {
		if it.DateCancelled.Valid {
			return inventorybiz.ReleaseInventoryItem{}, false
		}
		return inventorybiz.ReleaseInventoryItem{
			RefType: inventorydb.InventoryStockRefTypeProductSku,
			RefID:   it.SkuID,
			Amount:  it.Quantity,
		}, true
	})
	if len(releaseItems) > 0 {
		if err := b.Inventory().ReleaseInventory(ctx, inventorybiz.ReleaseInventoryParams{
			Items: releaseItems,
		}); err != nil {
			return zero, fmt.Errorf("release inventory: %w", err)
		}
	}

	return updated, nil
}
