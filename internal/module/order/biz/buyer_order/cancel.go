package buyerorder

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	accountbiz "shopnexus-server/internal/module/account/biz"
	accountmodel "shopnexus-server/internal/module/account/model"
	inventorybiz "shopnexus-server/internal/module/inventory/biz"
	inventorydb "shopnexus-server/internal/module/inventory/db/sqlc"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/shared/compensate"
	"shopnexus-server/internal/shared/idempotency"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
)

type CancelBuyerPendingParams struct {
	AccountID uuid.UUID `validate:"required"`
	ItemID    int64     `validate:"required"`
}

// CancelBuyerPending cancels a pre-confirm pending item.
func (b *BuyerHandler) CancelBuyerPending(ctx context.Context, params CancelBuyerPendingParams) error {
	if err := validator.Validate(params); err != nil {
		return fmt.Errorf("validate cancel pending item: %w", err)
	}

	item, err := func() (orderdb.OrderItem, error) {
		var zero orderdb.OrderItem
		dbItem, err := b.Storage.Querier().GetItem(ctx, null.IntFrom(params.ItemID))
		if err != nil {
			return zero, fmt.Errorf("db get item: %w", err)
		}
		if dbItem.AccountID != params.AccountID {
			return zero, ordermodel.ErrOrderItemNotFound
		}
		if dbItem.OrderID.Valid {
			return zero, ordermodel.ErrItemAlreadyConfirmed
		}
		if dbItem.DateCancelled.Valid {
			return zero, ordermodel.ErrItemAlreadyCancelled
		}
		return dbItem, nil
	}()
	if err != nil {
		return fmt.Errorf("fetch item: %w", err)
	}

	paymentSession, err := b.Storage.Querier().GetPaymentSession(ctx, uuid.NullUUID{UUID: item.PaymentSessionID, Valid: true})
	if err != nil {
		return fmt.Errorf("db get payment session: %w", err)
	}

	switch paymentSession.Status {
	case orderdb.OrderStatusPending:
		// Workflow is still in WaitFirst — signal cancel and let saga compensate.
		if err = b.checkout.Send().CancelCheckout(ctx, item.PaymentSessionID); err != nil {
			return fmt.Errorf("signal cancel checkout: %w", err)
		}

	case orderdb.OrderStatusSuccess:
		// Workflow exited; partial refund this single item.
		if err = b.RefundPendingItem(ctx, RefundPendingItemParams{
			Item:             item,
			PaymentSessionID: paymentSession.ID,
		}); err != nil {
			return fmt.Errorf("refund pending item: %w", err)
		}

	default:
		return ordermodel.ErrItemAlreadyCancelled
	}

	// Notify seller (fire-and-forget).
	if err = b.Notify(ctx, accountbiz.CreateNotificationParams{
		AccountID: item.SellerID,
		Type:      accountmodel.NotiPendingItemCancelled,
		Channel:   accountmodel.ChannelInApp,
		Title:     "Pending item cancelled",
		Content:   "A buyer has cancelled a pending item.",
	}); err != nil {
		return fmt.Errorf("notify seller: %w", err)
	}

	return nil
}

type RefundPendingItemParams struct {
	Item             orderdb.OrderItem
	PaymentSessionID uuid.UUID
}

// RefundPendingItem refunds a single paid item (not yet confirmed).
func (b *BuyerHandler) RefundPendingItem(
	ctx context.Context,
	params RefundPendingItemParams,
) error {
	sagaTx := compensate.New(ctx)

	return sagaTx.Wrap(func() error {
		var err error
		var buyerCurrency string
		buyerCurrency, err = b.InferCurrency(ctx, params.Item.AccountID)
		if err != nil {
			return fmt.Errorf("infer buyer currency: %w", err)
		}

		// Step 1: find the original positive Success tx — refund leg reverses it.
		// Single original tx per session (no split-tender).
		var originalTxID uuid.NullUUID
		originalTxID, err = func() (uuid.NullUUID, error) {
			var txs []orderdb.OrderTransaction
			txs, err = b.Storage.Querier().ListTransactionsBySession(ctx, params.PaymentSessionID)
			if err != nil {
				return uuid.NullUUID{}, err
			}
			if originalTx, ok := ordermodel.FindOriginalCharge(txs); ok {
				return uuid.NullUUID{UUID: originalTx.ID, Valid: true}, nil
			}
			return uuid.NullUUID{}, ordermodel.ErrOrderItemNotFound
		}()
		if err != nil {
			return fmt.Errorf("find original tx: %w", err)
		}

		// Step 2: release inventory
		// Saga key paired across forward (Release, claims) and compensator (Reserve, consumes).
		// deterministic key: retries must reuse it so the idempotency ledger dedupes
		releaseKey := uuid.NewSHA1(uuid.NameSpaceOID, fmt.Appendf(nil, "cancel-release:item:%d", params.Item.ID))
		sagaTx.Defer("reserve_inventory", func(ctx context.Context) error {
			_, e := b.inventory.Guaranteed().ReserveInventory(ctx, inventorybiz.ReserveInventoryParams{
				Keys: idempotency.Keys{ConsumeKey: releaseKey},
				Items: []inventorybiz.ReserveInventoryItem{{
					RefType: inventorydb.InventoryStockRefTypeProductSku,
					RefID:   params.Item.SkuID,
					Amount:  params.Item.Quantity,
				}},
			})
			return e
		})
		if err = b.inventory.Guaranteed().ReleaseInventory(ctx, inventorybiz.ReleaseInventoryParams{
			Keys: idempotency.Keys{ClaimKey: releaseKey},
			Items: []inventorybiz.ReleaseInventoryItem{{
				RefType: inventorydb.InventoryStockRefTypeProductSku,
				RefID:   params.Item.SkuID,
				Amount:  params.Item.Quantity,
			}},
		}); err != nil {
			return fmt.Errorf("release inventory: %w", err)
		}

		// Step 3: credit only the partial item amount
		// Compensator debits the same amount
		creditRef := fmt.Sprintf("partial-refund:item:%d", params.Item.ID)
		sagaTx.Defer("wallet_debit", func(ctx context.Context) error {
			_, e := b.account.Guaranteed().WalletDebit(ctx, accountbiz.WalletDebitParams{
				AccountID: params.Item.AccountID,
				Amount:    params.Item.TotalAmount,
				Reference: "rollback:" + creditRef,
				Note:      "rollback partial refund credit",
			})
			return e
		})
		if err = b.account.Guaranteed().WalletCredit(ctx, accountbiz.WalletCreditParams{
			AccountID: params.Item.AccountID,
			Amount:    params.Item.TotalAmount,
			Type:      "Refund",
			Reference: creditRef,
			Note:      "buyer cancel pre-confirm partial refund",
		}); err != nil {
			return fmt.Errorf("credit buyer wallet: %w", err)
		}

		// Step 4 (last, no compensator): atomic refund tx + cancel item.
		// deterministic key: retries must reuse it so the idempotency ledger dedupes
		refundTxID := uuid.NewSHA1(uuid.NameSpaceOID, fmt.Appendf(nil, "cancel-refund:item:%d", params.Item.ID))
		txStorage, e := b.Storage.BeginTx(ctx)
		if e != nil {
			return fmt.Errorf("begin tx: %w", e)
		}
		defer txStorage.Rollback(ctx)

		if _, e = txStorage.Querier().CreateDefaultTransaction(ctx, orderdb.CreateDefaultTransactionParams{
			ID:          refundTxID,
			SessionID:   params.PaymentSessionID,
			Status:      orderdb.OrderStatusSuccess,
			Note:        "buyer cancel pre-confirm",
			Data:        json.RawMessage("{}"),
			Amount:      -params.Item.TotalAmount,
			Currency:    buyerCurrency,
			ReversesID:  originalTxID,
			DateSettled: null.TimeFrom(time.Now()),
		}); e != nil {
			return fmt.Errorf("db create refund tx: %w", e)
		}
		if _, e = txStorage.Querier().CancelItem(ctx, orderdb.CancelItemParams{
			CancelledByID: uuid.NullUUID{UUID: params.Item.AccountID, Valid: true},
			ID:            params.Item.ID,
		}); e != nil {
			return fmt.Errorf("db cancel item: %w", e)
		}

		if err = txStorage.Commit(ctx); err != nil {
			return fmt.Errorf("refund tx + cancel item: %w", err)
		}

		return nil
	})
}
