package orderbiz

import (
	"encoding/json"
	"fmt"
	accountbiz "shopnexus-server/internal/module/account/biz"
	accountmodel "shopnexus-server/internal/module/account/model"
	inventorybiz "shopnexus-server/internal/module/inventory/biz"
	inventorydb "shopnexus-server/internal/module/inventory/db/sqlc"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/shared/idempotency"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/saga"
	"shopnexus-server/internal/shared/validator"
	"time"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	restate "github.com/restatedev/sdk-go"
	"github.com/samber/lo"
)

// ListBuyerPendingOrders returns orders that are post-confirm but neither
// completed (payout released) nor cancelled. Includes orders awaiting
// shipment, in transit, delivered-but-not-paid-out.
func (b *buyerHandler) ListBuyerPendingOrders(
	ctx restate.Context,
	params ListBuyerPendingOrdersParams,
) (paginate.PaginateResult[ordermodel.Order], error) {
	return b.listBuyerOrders(ctx, params.Params, params.BuyerID, func(rctx restate.Context, p orderListPage) ([]orderdb.OrderOrder, int64, error) {
		rows, err := b.storage.Querier().ListBuyerPendingOrders(rctx, orderdb.ListBuyerPendingOrdersParams{
			BuyerID: p.BuyerID,
			Limit:   p.Limit,
			Offset:  p.Offset,
		})
		if err != nil {
			return nil, 0, err
		}
		orders := lo.Map(rows, func(r orderdb.ListBuyerPendingOrdersRow, _ int) orderdb.OrderOrder { return r.OrderOrder })
		var total int64
		if len(rows) > 0 {
			total = rows[0].TotalCount
		}
		return orders, total, nil
	})
}

// ListBuyerCompletedOrders returns orders whose seller payout has been
// released (escrow done). Delivered-but-not-paid-out orders stay Pending.
func (b *buyerHandler) ListBuyerCompletedOrders(
	ctx restate.Context,
	params ListBuyerCompletedOrdersParams,
) (paginate.PaginateResult[ordermodel.Order], error) {
	return b.listBuyerOrders(ctx, params.Params, params.BuyerID, func(rctx restate.Context, p orderListPage) ([]orderdb.OrderOrder, int64, error) {
		rows, err := b.storage.Querier().ListBuyerCompletedOrders(rctx, orderdb.ListBuyerCompletedOrdersParams{
			BuyerID: p.BuyerID,
			Limit:   p.Limit,
			Offset:  p.Offset,
		})
		if err != nil {
			return nil, 0, err
		}
		orders := lo.Map(rows, func(r orderdb.ListBuyerCompletedOrdersRow, _ int) orderdb.OrderOrder { return r.OrderOrder })
		var total int64
		if len(rows) > 0 {
			total = rows[0].TotalCount
		}
		return orders, total, nil
	})
}

// ListBuyerCancelledOrders returns orders where any of confirm/transport/payout
// is in a Failed or Cancelled state.
func (b *buyerHandler) ListBuyerCancelledOrders(
	ctx restate.Context,
	params ListBuyerCancelledOrdersParams,
) (paginate.PaginateResult[ordermodel.Order], error) {
	return b.listBuyerOrders(ctx, params.Params, params.BuyerID, func(rctx restate.Context, p orderListPage) ([]orderdb.OrderOrder, int64, error) {
		rows, err := b.storage.Querier().ListBuyerCancelledOrders(rctx, orderdb.ListBuyerCancelledOrdersParams{
			BuyerID: p.BuyerID,
			Limit:   p.Limit,
			Offset:  p.Offset,
		})
		if err != nil {
			return nil, 0, err
		}
		orders := lo.Map(rows, func(r orderdb.ListBuyerCancelledOrdersRow, _ int) orderdb.OrderOrder { return r.OrderOrder })
		var total int64
		if len(rows) > 0 {
			total = rows[0].TotalCount
		}
		return orders, total, nil
	})
}

// ListBuyerCancelledItems returns pre-confirm items that died before becoming
// orders: failed/cancelled checkout sessions, or individually-refunded items
// from a Success session (date_cancelled set).
func (b *buyerHandler) ListBuyerCancelledItems(
	ctx restate.Context,
	params ListBuyerCancelledItemsParams,
) (paginate.PaginateResult[ordermodel.OrderItem], error) {
	var zero paginate.PaginateResult[ordermodel.OrderItem]
	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate list cancelled items: %w", err)
	}
	return b.listBuyerItems(ctx, params.Params, params.AccountID,
		func(rctx restate.Context, accountID uuid.UUID) ([]orderdb.OrderItem, int64, error) {
			items, err := b.storage.Querier().ListBuyerCancelledItems(rctx, accountID)
			if err != nil {
				return nil, 0, err
			}
			total, err := b.storage.Querier().CountBuyerCancelledItems(rctx, accountID)
			if err != nil {
				return nil, 0, err
			}
			return items, total, nil
		})
}

// orderListPage carries the per-page args into the per-query closure.
type orderListPage struct {
	BuyerID uuid.UUID
	Limit   null.Int32
	Offset  null.Int32
}

// listBuyerOrders is the shared backbone for the three order-list endpoints:
// validate -> run query in restate.Run -> hydrate -> wrap in PaginateResult.
func (b *buyerHandler) listBuyerOrders(
	ctx restate.Context,
	pagination paginate.Params,
	buyerID uuid.UUID,
	fetch func(restate.Context, orderListPage) ([]orderdb.OrderOrder, int64, error),
) (paginate.PaginateResult[ordermodel.Order], error) {
	var zero paginate.PaginateResult[ordermodel.Order]
	if err := validator.Validate(struct {
		BuyerID uuid.UUID `validate:"required"`
	}{BuyerID: buyerID}); err != nil {
		return zero, fmt.Errorf("validate list orders: %w", err)
	}

	orders, total, err := fetch(ctx, orderListPage{
		BuyerID: buyerID,
		Limit:   pagination.Limit,
		Offset:  pagination.Offset(),
	})
	if err != nil {
		return zero, fmt.Errorf("db list orders: %w", err)
	}

	data, err := b.hydrateOrders(ctx, orders)
	if err != nil {
		return zero, fmt.Errorf("hydrate orders: %w", err)
	}

	var totalVal null.Int64
	totalVal.SetValid(total)
	return paginate.PaginateResult[ordermodel.Order]{
		PageParams: pagination,
		Total:      totalVal,
		Data:       data,
	}, nil
}

// listBuyerItems is the shared backbone for buyer item-list endpoints.
// Mirrors the existing ListBuyerPendingItems shape including session attach.
func (b *buyerHandler) listBuyerItems(
	ctx restate.Context,
	pagination paginate.Params,
	accountID uuid.UUID,
	fetch func(restate.Context, uuid.UUID) ([]orderdb.OrderItem, int64, error),
) (paginate.PaginateResult[ordermodel.OrderItem], error) {
	var zero paginate.PaginateResult[ordermodel.OrderItem]

	items, total, err := fetch(ctx, accountID)
	if err != nil {
		return zero, fmt.Errorf("db list items: %w", err)
	}

	enriched, err := b.enrichItems(ctx, items)
	if err != nil {
		return zero, fmt.Errorf("enrich items: %w", err)
	}

	if len(enriched) > 0 {
		sessionIDs := lo.Uniq(lo.Map(enriched, func(it ordermodel.OrderItem, _ int) uuid.UUID { return it.PaymentSessionID }))
		var sessions []orderdb.OrderPaymentSession
		sessions, err = b.storage.Querier().ListPaymentSession(ctx, orderdb.ListPaymentSessionParams{ID: sessionIDs})
		if err != nil {
			return zero, fmt.Errorf("db fetch payment sessions: %w", err)
		}
		sessionMap := lo.KeyBy(sessions, func(s orderdb.OrderPaymentSession) uuid.UUID { return s.ID })
		for i := range enriched {
			if s, ok := sessionMap[enriched[i].PaymentSessionID]; ok {
				mapped := mapPaymentSession(s)
				enriched[i].PaymentSession = &mapped
			}
		}
	}

	var totalVal null.Int64
	totalVal.SetValid(total)
	return paginate.PaginateResult[ordermodel.OrderItem]{
		PageParams: pagination,
		Total:      totalVal,
		Data:       enriched,
	}, nil
}

// ListBuyerPendingItems returns paginated paid pending items for the buyer.
func (b *buyerHandler) ListBuyerPendingItems(
	ctx restate.Context,
	params ListBuyerPendingItemsParams,
) (paginate.PaginateResult[ordermodel.OrderItem], error) {
	if err := validator.Validate(params); err != nil {
		return paginate.PaginateResult[ordermodel.OrderItem]{}, fmt.Errorf("validate list pending items: %w", err)
	}
	return b.listBuyerItems(ctx, params.Params, params.AccountID,
		func(rctx restate.Context, accountID uuid.UUID) ([]orderdb.OrderItem, int64, error) {
			items, err := b.storage.Querier().ListBuyerPendingItems(rctx, accountID)
			if err != nil {
				return nil, 0, err
			}
			total, err := b.storage.Querier().CountBuyerPendingItems(rctx, accountID)
			if err != nil {
				return nil, 0, err
			}
			return items, total, nil
		})
}

// CancelBuyerPending cancels a pre-confirm pending item.
func (b *buyerHandler) CancelBuyerPending(ctx restate.Context, params CancelBuyerPendingParams) error {
	if err := validator.Validate(params); err != nil {
		return fmt.Errorf("validate cancel pending item: %w", err)
	}

	item, err := restate.Run(ctx, func(ctx restate.RunContext) (orderdb.OrderItem, error) {
		var zero orderdb.OrderItem
		dbItem, err := b.storage.Querier().GetItem(ctx, null.IntFrom(params.ItemID))
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
	})
	if err != nil {
		return fmt.Errorf("fetch item: %w", err)
	}

	paymentSession, err := restate.Run(ctx, func(ctx restate.RunContext) (orderdb.OrderPaymentSession, error) {
		return b.storage.Querier().GetPaymentSession(ctx, uuid.NullUUID{UUID: item.PaymentSessionID, Valid: true})
	})
	if err != nil {
		return fmt.Errorf("db get payment session: %w", err)
	}

	switch paymentSession.Status {
	case orderdb.OrderStatusPending:
		// Workflow is still in WaitFirst — signal cancel and let saga compensate.
		restate.WorkflowSend(ctx, "CheckoutWorkflow", item.PaymentSessionID.String(), "CancelCheckout").
			Send(struct{}{})

	case orderdb.OrderStatusSuccess:
		// Workflow exited; partial refund this single item.
		if err = b.RefundPendingItem(ctx, RefundPendingItemParams{
			Item:             item,
			PaymentSessionID: paymentSession.ID,
		}); err != nil {
			return err
		}

	default:
		return ordermodel.ErrItemAlreadyCancelled
	}

	// Notify seller (fire-and-forget).
	restate.ServiceSend(ctx, "Account", "CreateNotification").Send(accountbiz.CreateNotificationParams{
		AccountID: item.SellerID,
		Type:      accountmodel.NotiPendingItemCancelled,
		Channel:   accountmodel.ChannelInApp,
		Title:     "Pending item cancelled",
		Content:   "A buyer has cancelled a pending item.",
	})

	return nil
}

type RefundPendingItemParams struct {
	Item             orderdb.OrderItem
	PaymentSessionID uuid.UUID
}

// RefundPendingItem refunds a single paid item (not yet confirmed).
func (b *buyerHandler) RefundPendingItem(
	ctx restate.Context,
	params RefundPendingItemParams,
) error {
	sagaTx := saga.New(ctx)

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
		originalTxID, err = restate.Run(ctx, func(rctx restate.RunContext) (uuid.NullUUID, error) {
			var txs []orderdb.OrderTransaction
			txs, err = b.storage.Querier().ListTransactionsBySession(rctx, params.PaymentSessionID)
			if err != nil {
				return uuid.NullUUID{}, err
			}
			if originalTx, ok := findOriginalCharge(txs); ok {
				return uuid.NullUUID{UUID: originalTx.ID, Valid: true}, nil
			}
			return uuid.NullUUID{}, ordermodel.ErrOrderItemNotFound
		})
		if err != nil {
			return fmt.Errorf("find original tx: %w", err)
		}

		// Step 2: release inventory
		// Saga key paired across forward (Release, claims) and compensator (Reserve, consumes).
		releaseKey := restate.UUID(ctx)
		sagaTx.Defer("reserve_inventory", func(ctx restate.Context) error {
			return restate.RunVoid(ctx, func(rctx restate.RunContext) error {
				_, e := b.inventory.ReserveInventory(rctx, inventorybiz.ReserveInventoryParams{
					Keys: idempotency.Keys{ConsumeKey: releaseKey},
					Items: []inventorybiz.ReserveInventoryItem{{
						RefType: inventorydb.InventoryStockRefTypeProductSku,
						RefID:   params.Item.SkuID,
						Amount:  params.Item.Quantity,
					}},
				})
				return e
			})
		})
		if err = b.inventory.ReleaseInventory(ctx, inventorybiz.ReleaseInventoryParams{
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
		sagaTx.Defer("wallet_debit", func(ctx restate.Context) error {
			return restate.RunVoid(ctx, func(rctx restate.RunContext) error {
				_, e := b.account.WalletDebit(rctx, accountbiz.WalletDebitParams{
					AccountID: params.Item.AccountID,
					Amount:    params.Item.TotalAmount,
					Reference: "rollback:" + creditRef,
					Note:      "rollback partial refund credit",
				})
				return e
			})
		})
		if err = b.account.WalletCredit(ctx, accountbiz.WalletCreditParams{
			AccountID: params.Item.AccountID,
			Amount:    params.Item.TotalAmount,
			Type:      "Refund",
			Reference: creditRef,
			Note:      "buyer cancel pre-confirm partial refund",
		}); err != nil {
			return fmt.Errorf("credit buyer wallet: %w", err)
		}

		// Step 4 (last, no compensator): atomic refund tx + cancel item.
		refundTxID := restate.UUID(ctx)
		if err = restate.RunVoid(ctx, func(rctx restate.RunContext) error {
			txStorage, e := b.storage.BeginTx(rctx)
			if e != nil {
				return fmt.Errorf("begin tx: %w", e)
			}

			if _, e = txStorage.Querier().CreateDefaultTransaction(rctx, orderdb.CreateDefaultTransactionParams{
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
			if _, e = txStorage.Querier().CancelItem(rctx, orderdb.CancelItemParams{
				CancelledByID: uuid.NullUUID{UUID: params.Item.AccountID, Valid: true},
				ID:            params.Item.ID,
			}); e != nil {
				return fmt.Errorf("db cancel item: %w", e)
			}

			return txStorage.Commit(rctx)
		}); err != nil {
			return fmt.Errorf("refund tx + cancel item: %w", err)
		}

		return nil
	})
}

type GetCheckoutSummaryParams struct {
	AccountID uuid.UUID `validate:"required"`
	TxID      uuid.UUID `validate:"required"`
}

func (b *buyerHandler) GetCheckoutSummary(
	ctx restate.Context,
	params GetCheckoutSummaryParams,
) (ordermodel.CheckoutSummary, error) {
	var zero ordermodel.CheckoutSummary

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate checkout summary: %w", err)
	}

	tx, err := b.storage.Querier().GetTransaction(ctx, uuid.NullUUID{UUID: params.TxID, Valid: true})
	if err != nil {
		return zero, fmt.Errorf("get transaction: %w", err)
	}

	session, err := b.storage.Querier().GetPaymentSession(ctx, uuid.NullUUID{UUID: tx.SessionID, Valid: true})
	if err != nil {
		return zero, fmt.Errorf("get payment session: %w", err)
	}

	dbItems, err := b.storage.Querier().ListItemsByPaymentSession(ctx, session.ID)
	if err != nil {
		return zero, fmt.Errorf("list session items: %w", err)
	}

	// Authorize: only the owner may read the summary.
	for _, it := range dbItems {
		if it.AccountID != params.AccountID {
			return zero, ordermodel.ErrOrderItemNotFound
		}
	}

	enriched, err := b.enrichItems(ctx, dbItems)
	if err != nil {
		return zero, fmt.Errorf("enrich items: %w", err)
	}

	items := make([]ordermodel.CheckoutSummaryItem, 0, len(enriched))
	for _, it := range enriched {
		items = append(items, ordermodel.CheckoutSummaryItem{
			ID:          it.ID,
			SkuID:       it.SkuID,
			SpuID:       it.SpuID,
			Slug:        it.Slug,
			SkuName:     it.SkuName,
			Quantity:    it.Quantity,
			TotalAmount: it.TotalAmount,
			Currency:    session.Currency,
			ImageURL:    it.ImageURL,
		})
	}

	return ordermodel.CheckoutSummary{
		Session: mapPaymentSession(session),
		Items:   items,
	}, nil
}

type ListBuyerPendingItemsParams struct {
	paginate.Params

	AccountID uuid.UUID `validate:"required"`
}

type CancelBuyerPendingParams struct {
	AccountID uuid.UUID `validate:"required"`
	ItemID    int64     `validate:"required"`
}

type ListBuyerPendingOrdersParams struct {
	paginate.Params

	BuyerID uuid.UUID `json:"buyer_id" validate:"required"`
}

type ListBuyerCompletedOrdersParams struct {
	paginate.Params

	BuyerID uuid.UUID `json:"buyer_id" validate:"required"`
}

type ListBuyerCancelledOrdersParams struct {
	paginate.Params

	BuyerID uuid.UUID `json:"buyer_id" validate:"required"`
}

type ListBuyerCancelledItemsParams struct {
	paginate.Params

	AccountID uuid.UUID `json:"account_id" validate:"required"`
}
