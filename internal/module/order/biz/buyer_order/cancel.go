package buyerorder

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
	"shopnexus-server/internal/shared/idempotency"
	"shopnexus-server/internal/shared/pgsqlc"
	"shopnexus-server/internal/shared/saga"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	restate "github.com/restatedev/sdk-go"
)

type CancelBuyerPendingParams struct {
	AccountID uuid.UUID `validate:"required"`
	ItemID    int64     `validate:"required"`
}

// CancelBuyerPending cancels a pre-confirm pending item.
func (b *BuyerHandler) CancelBuyerPending(ctx restate.Context, params CancelBuyerPendingParams) error {
	if err := validator.Validate(params); err != nil {
		return fmt.Errorf("validate cancel pending item: %w", err)
	}

	// decision: load + validate the item and its payment session.
	type decision struct {
		Item    ordermodel.OrderItem
		Session ordermodel.PaymentSession
	}
	dec, err := restate.Run(ctx, func(rctx restate.RunContext) (decision, error) {
		var zero decision
		dbItem, err := b.Storage.Querier().GetItem(rctx, params.ItemID)
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
		session, err := b.Storage.Querier().GetPaymentSession(rctx, dbItem.PaymentSessionID)
		if err != nil {
			return zero, fmt.Errorf("db get payment session: %w", err)
		}
		return decision{Item: dbItem, Session: session}, nil
	})
	if err != nil {
		return fmt.Errorf("fetch item: %w", err)
	}
	item := dec.Item

	// execution: signal cancel (workflow still running) or refund the single
	// settled item (workflow exited).
	switch dec.Session.Status {
	case ordermodel.StatusPending:
		// Workflow is still in WaitFirst — signal cancel and let saga compensate.
		// The cross-workflow Send self-journals.
		if err = b.checkout.Send().CancelCheckout(ctx, item.PaymentSessionID); err != nil {
			return fmt.Errorf("signal cancel checkout: %w", err)
		}

	case ordermodel.StatusSuccess:
		// Workflow exited; partial refund this single item.
		if err = b.RefundPendingItem(ctx, RefundPendingItemParams{
			Item:             item,
			PaymentSessionID: dec.Session.ID,
		}); err != nil {
			return fmt.Errorf("refund pending item: %w", err)
		}

	case ordermodel.StatusProcessing, ordermodel.StatusCancelled, ordermodel.StatusFailed:
		return ordermodel.ErrItemAlreadyCancelled
	}

	// tail: notify seller (fire-and-forget). The cross-module Send self-journals.
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
	Item             ordermodel.OrderItem
	PaymentSessionID uuid.UUID
}

// RefundPendingItem refunds a single paid item (not yet confirmed).
func (b *BuyerHandler) RefundPendingItem(
	ctx restate.Context,
	params RefundPendingItemParams,
) error {
	var err error

	// decision: infer the buyer currency and find the original settled charge
	// that the refund leg reverses (single original tx per session, no split-tender).
	type plan struct {
		Currency   string
		OriginalTx uuid.NullUUID
	}
	dec, err := restate.Run(ctx, func(rctx restate.RunContext) (plan, error) {
		var zero plan
		currency, e := b.InferCurrency(rctx, params.Item.AccountID)
		if e != nil {
			return zero, fmt.Errorf("infer buyer currency: %w", e)
		}
		txs, e := b.Storage.Querier().ListTransactionsBySession(rctx, params.PaymentSessionID)
		if e != nil {
			return zero, fmt.Errorf("list session txs: %w", e)
		}
		originalTx, ok := ordermodel.FindOriginalCharge(txs)
		if !ok {
			return zero, ordermodel.ErrOrderItemNotFound
		}
		return plan{Currency: currency, OriginalTx: uuid.NullUUID{UUID: originalTx.ID, Valid: true}}, nil
	})
	if err != nil {
		return fmt.Errorf("plan refund: %w", err)
	}

	// execution: release inventory + credit wallet under a saga, then atomically
	// write the refund tx + cancel the item. Cross-module steps register a
	// compensator BEFORE they run so a terminal failure rolls them back LIFO.
	sagaTx := saga.New(ctx)
	err = sagaTx.Wrap(func() error {
		// Step 1: release inventory.
		// Saga key paired across forward (Release, claims) and compensator (Reserve, consumes).
		// deterministic key: retries must reuse it so the idempotency ledger dedupes
		releaseKey := uuid.NewSHA1(uuid.NameSpaceOID, fmt.Appendf(nil, "cancel-release:item:%d", params.Item.ID))
		sagaTx.Defer("reserve_inventory", func(rctx restate.Context) error {
			_, e := b.inventory.Call().ReserveInventory(rctx, inventorybiz.ReserveInventoryParams{
				Keys: idempotency.Keys{ConsumeKey: releaseKey},
				Items: []inventorybiz.ReserveInventoryItem{{
					RefType: inventorydb.InventoryStockRefTypeProductSku,
					RefID:   params.Item.SkuID,
					Amount:  params.Item.Quantity,
				}},
			})
			return e
		})
		if e := b.inventory.Call().ReleaseInventory(ctx, inventorybiz.ReleaseInventoryParams{
			Keys: idempotency.Keys{ClaimKey: releaseKey},
			Items: []inventorybiz.ReleaseInventoryItem{{
				RefType: inventorydb.InventoryStockRefTypeProductSku,
				RefID:   params.Item.SkuID,
				Amount:  params.Item.Quantity,
			}},
		}); e != nil {
			return fmt.Errorf("release inventory: %w", e)
		}

		// Step 2: credit only the partial item amount. Compensator debits the same amount.
		creditRef := fmt.Sprintf("partial-refund:item:%d", params.Item.ID)
		sagaTx.Defer("wallet_debit", func(rctx restate.Context) error {
			_, e := b.account.Call().WalletDebit(rctx, accountbiz.WalletDebitParams{
				AccountID: params.Item.AccountID,
				Amount:    params.Item.TotalAmount,
				Reference: "rollback:" + creditRef,
				Note:      "rollback partial refund credit",
			})
			return e
		})
		if e := b.account.Call().WalletCredit(ctx, accountbiz.WalletCreditParams{
			AccountID: params.Item.AccountID,
			Amount:    params.Item.TotalAmount,
			Type:      "Refund",
			Reference: creditRef,
			Note:      "buyer cancel pre-confirm partial refund",
		}); e != nil {
			return fmt.Errorf("credit buyer wallet: %w", e)
		}

		// Step 3 (last, no compensator): atomic refund tx + cancel item.
		// deterministic key: retries must reuse it so the idempotency ledger dedupes
		refundTxID := uuid.NewSHA1(uuid.NameSpaceOID, fmt.Appendf(nil, "cancel-refund:item:%d", params.Item.ID))
		if e := restate.RunVoid(ctx, func(rctx restate.RunContext) error {
			return b.Storage.Transact(rctx, func(s pgsqlc.Storage[*orderrepo.Repository]) error {
				if _, te := s.Querier().CreateTransaction(rctx, orderrepo.CreateTransactionParams{
					ID:          refundTxID,
					SessionID:   params.PaymentSessionID,
					Status:      ordermodel.StatusSuccess,
					Note:        "buyer cancel pre-confirm",
					Data:        json.RawMessage("{}"),
					Amount:      -params.Item.TotalAmount,
					Currency:    dec.Currency,
					ReversesID:  dec.OriginalTx,
					DateSettled: null.TimeFrom(time.Now()),
				}); te != nil {
					return fmt.Errorf("db create refund tx: %w", te)
				}
				if _, te := s.Querier().CancelItem(rctx, orderrepo.CancelItemParams{
					CancelledByID: uuid.NullUUID{UUID: params.Item.AccountID, Valid: true},
					ID:            params.Item.ID,
				}); te != nil {
					return fmt.Errorf("db cancel item: %w", te)
				}
				return nil
			})
		}); e != nil {
			return e
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}
