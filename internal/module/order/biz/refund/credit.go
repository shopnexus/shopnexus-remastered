package refund

import (
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
	restate "github.com/restatedev/sdk-go"
	"github.com/samber/lo"
)

// CreditFromSession credits the recipient with the sum of positive-amount Success
// transactions in the given session. Use this when a session is being voided or
// refunded — credits only legs that actually settled, never minting balance for
// unsettled / failed / pending legs. Returns the amount credited; 0 means no-op.
func (b *RefundHandler) CreditFromSession(
	ctx restate.Context,
	params ordermodel.CreditFromSessionParams,
) (int64, error) {
	// decision: sum the settled positive legs in the session.
	settled, err := restate.Run(ctx, func(rctx restate.RunContext) (int64, error) {
		txs, err := b.Storage.Querier().ListTransactionsBySession(rctx, params.SessionID)
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
	})
	if err != nil {
		return 0, fmt.Errorf("read session txs: %w", err)
	}
	if settled == 0 {
		return 0, nil
	}

	// execution: credit the buyer wallet. Cross-module Call self-journals.
	if err = b.Account().Call().WalletCredit(ctx, accountbiz.WalletCreditParams{
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
//
// TODO(idempotency): the deterministic refundTxID + ON CONFLICT DO NOTHING on
// CreateDefaultTransaction (load-existing on conflict) would make the DB write
// safe at every retry level. Until then the journaled Run below guards replay,
// but a fresh retry after journal trim would re-run the DB writes.
func (b *RefundHandler) ExecuteRefundCredit(
	ctx restate.Context,
	refund orderdb.OrderRefund,
	deciderID uuid.UUID,
	reason ordermodel.RefundCreditReason,
) (orderdb.OrderRefund, error) {
	var zero orderdb.OrderRefund

	// decision: gather the order, non-cancelled items, currency and original
	// charge needed to mint the reversing refund leg.
	type plan struct {
		Order        orderdb.OrderOrder
		Items        []orderdb.OrderItem
		AnyItem      orderdb.OrderItem
		RefundAmount int64
		Currency     string
		OriginalTxID uuid.NullUUID
	}
	dec, err := restate.Run(ctx, func(rctx restate.RunContext) (plan, error) {
		var zero plan
		res, err := b.Storage.Querier().ListItem(rctx, orderdb.ListItemParams{
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

		order, err := b.Storage.Querier().GetOrder(rctx, orderdb.GetOrderParams{
			ID: uuid.NullUUID{UUID: refund.OrderID, Valid: true},
		})
		if err != nil {
			return zero, fmt.Errorf("get order: %w", err)
		}

		buyerCurrency, err := b.InferCurrency(rctx, order.BuyerID)
		if err != nil {
			return zero, fmt.Errorf("infer currency: %w", err)
		}

		sessionTxs, err := b.Storage.Querier().ListTransactionsBySession(rctx, anyItem.PaymentSessionID)
		if err != nil {
			return zero, fmt.Errorf("list session txs: %w", err)
		}
		originalTx, ok := ordermodel.FindOriginalCharge(sessionTxs)
		if !ok {
			return zero, fmt.Errorf("no original tx: %w", ordermodel.ErrOrderItemNotFound)
		}

		return plan{
			Order:        order,
			Items:        items,
			AnyItem:      anyItem,
			RefundAmount: refundAmount,
			Currency:     buyerCurrency,
			OriginalTxID: uuid.NullUUID{UUID: originalTx.ID, Valid: true},
		}, nil
	})
	if err != nil {
		return zero, err
	}

	// deterministic key: retries must reuse it so the idempotency ledger dedupes
	refundTxID := uuid.NewSHA1(uuid.NameSpaceOID, fmt.Appendf(nil, "refund-credit:refund:%s", refund.ID))

	// execution: mint the refund tx, flip the refund status and cancel each item.
	updated, err := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderRefund, error) {
		refundTx, e := b.Storage.Querier().CreateDefaultTransaction(rctx, orderdb.CreateDefaultTransactionParams{
			ID:            refundTxID,
			SessionID:     dec.AnyItem.PaymentSessionID,
			Status:        orderdb.OrderStatusSuccess,
			Note:          fmt.Sprintf("refund %s: %s", refund.ID, reason),
			Error:         null.String{},
			PaymentOption: null.String{},
			Data:          json.RawMessage("{}"),
			Amount:        -dec.RefundAmount,
			Currency:      dec.Currency,
			ReversesID:    dec.OriginalTxID,
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
			u, e = b.Storage.Querier().SellerApproveRefund(rctx, orderdb.SellerApproveRefundParams{
				ID:         refund.ID,
				RefundTxID: uuid.NullUUID{UUID: refundTx.ID, Valid: true},
			})
		case orderdb.OrderRefundStatusDisputed:
			u, e = b.Storage.Querier().AdminDismissDispute(rctx, orderdb.AdminDismissDisputeParams{
				ID:         refund.ID,
				RefundTxID: uuid.NullUUID{UUID: refundTx.ID, Valid: true},
			})
		default:
			return orderdb.OrderRefund{}, ordermodel.ErrRefundWrongStage
		}
		if e != nil {
			return orderdb.OrderRefund{}, fmt.Errorf("approve refund: %w", e)
		}

		for _, it := range dec.Items {
			if it.DateCancelled.Valid {
				continue
			}
			if _, ce := b.Storage.Querier().CancelItem(rctx, orderdb.CancelItemParams{
				ID:            it.ID,
				CancelledByID: uuid.NullUUID{UUID: deciderID, Valid: true},
			}); ce != nil {
				return orderdb.OrderRefund{}, fmt.Errorf("cancel item: %w", ce)
			}
		}
		return u, nil
	})
	if err != nil {
		return zero, fmt.Errorf("execute refund ops: %w", err)
	}

	// execution: credit the buyer wallet for the settled session amount.
	if _, err := b.CreditFromSession(ctx, ordermodel.CreditFromSessionParams{
		SessionID:  dec.AnyItem.PaymentSessionID,
		AccountID:  dec.Order.BuyerID,
		CreditType: "Refund",
		Reference:  fmt.Sprintf("refund:%s", refund.ID),
		Note:       fmt.Sprintf("refund accepted (%s)", reason),
	}); err != nil {
		return zero, fmt.Errorf("wallet credit: %w", err)
	}

	// execution: restock inventory for every item we just cancelled. Mirrors the
	// release path in CancelBuyerPending / RejectSellerPending so the SKU quantity
	// goes back up when the refund settles. The inventory module owns its own
	// idempotency; the cross-module Call self-journals.
	releaseItems := lo.FilterMap(dec.Items, func(it orderdb.OrderItem, _ int) (inventorybiz.ReleaseInventoryItem, bool) {
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
		if err := b.Inventory().Call().ReleaseInventory(ctx, inventorybiz.ReleaseInventoryParams{
			Items: releaseItems,
		}); err != nil {
			return zero, fmt.Errorf("release inventory: %w", err)
		}
	}

	return updated, nil
}
