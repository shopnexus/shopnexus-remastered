package orderbiz

import (
	"context"
	"encoding/json"
	"fmt"
	accountbiz "shopnexus-server/internal/module/account/biz"
	catalogbiz "shopnexus-server/internal/module/catalog/biz"
	catalogmodel "shopnexus-server/internal/module/catalog/model"
	commonbiz "shopnexus-server/internal/module/common/biz"
	commondb "shopnexus-server/internal/module/common/db/sqlc"
	inventorybiz "shopnexus-server/internal/module/inventory/biz"
	inventorydb "shopnexus-server/internal/module/inventory/db/sqlc"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
	sharedcurrency "shopnexus-server/internal/shared/currency"
	"time"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	restate "github.com/restatedev/sdk-go"
	"github.com/samber/lo"
)

// InferCurrency fetches the profile for accountID and resolves its ISO 4217 currency code.
func (b *core) InferCurrency(ctx context.Context, accountID uuid.UUID) (string, error) {
	prof, err := b.account.GetProfile(ctx, accountbiz.GetProfileParams{AccountID: accountID})
	if err != nil {
		return "", fmt.Errorf("get profile for currency: %w", err)
	}
	cur, err := sharedcurrency.Infer(prof.Country)
	if err != nil {
		return "", fmt.Errorf("infer currency: %w", err)
	}
	return cur, nil
}

// enrichItems converts DB items to model items and hydrates each with its
// product slug + primary image so the FE can render cards without N hops.
func (b *core) enrichItems(ctx restate.Context, dbItems []orderdb.OrderItem) ([]ordermodel.OrderItem, error) {
	if len(dbItems) == 0 {
		return []ordermodel.OrderItem{}, nil
	}

	skuIDs := lo.Uniq(lo.Map(dbItems, func(it orderdb.OrderItem, _ int) uuid.UUID { return it.SkuID }))
	skus, err := b.catalog.ListProductSku(ctx, catalogbiz.ListProductSkuParams{ID: skuIDs})
	if err != nil {
		return nil, fmt.Errorf("enrich items: list skus: %w", err)
	}
	skuMap := lo.KeyBy(skus, func(s catalogmodel.ProductSku) uuid.UUID { return s.ID })

	spuIDs := lo.Uniq(lo.Map(skus, func(s catalogmodel.ProductSku, _ int) uuid.UUID { return s.SpuID }))
	listSpu, err := b.catalog.ListProductSpu(ctx, catalogbiz.ListProductSpuParams{ID: spuIDs})
	if err != nil {
		return nil, fmt.Errorf("enrich items: list spus: %w", err)
	}
	spuMap := lo.KeyBy(listSpu.Data, func(s catalogmodel.ProductSpu) uuid.UUID { return s.ID })

	resources, err := b.common.GetResources(ctx, commonbiz.GetResourcesParams{
		RefType: commondb.CommonResourceRefTypeProductSpu,
		RefIDs:  spuIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("enrich items: get resources: %w", err)
	}

	result := make([]ordermodel.OrderItem, 0, len(dbItems))
	for _, it := range dbItems {
		mapped := mapOrderItem(it)
		spuID := it.SpuID
		if sku, ok := skuMap[it.SkuID]; ok {
			spuID = sku.SpuID
		}
		if spu, ok := spuMap[spuID]; ok {
			mapped.Slug = spu.Slug
		}
		if res, ok := resources[spuID]; ok && len(res) > 0 {
			mapped.ImageURL = res[0].Url
		}
		result = append(result, mapped)
	}

	return result, nil
}

// hydrateOrders fans out one DB pull for items + transports per page, enriches
// items with product/resource data, then enriches each order with its confirm +
// payout session and total amount. The payout session loads regardless of
// status so the FE can render "Funds released" once it reaches Success.
func (b *core) hydrateOrders(ctx restate.Context, orders []orderdb.OrderOrder) ([]ordermodel.Order, error) {
	if len(orders) == 0 {
		return []ordermodel.Order{}, nil
	}

	orderIDs := lo.Map(orders, func(o orderdb.OrderOrder, _ int) uuid.UUID { return o.ID })
	transportIDs := lo.Uniq(lo.Map(orders, func(o orderdb.OrderOrder, _ int) int64 { return o.TransportID }))

	orderItems, err := b.storage.Querier().ListItem(ctx, orderdb.ListItemParams{
		OrderID: lo.Map(orderIDs, func(id uuid.UUID, _ int) uuid.NullUUID {
			return uuid.NullUUID{UUID: id, Valid: true}
		}),
	})
	if err != nil {
		return nil, fmt.Errorf("db fetch order items: %w", err)
	}
	transports, err := b.storage.Querier().ListTransport(ctx, orderdb.ListTransportParams{ID: transportIDs})
	if err != nil {
		return nil, fmt.Errorf("db fetch transports: %w", err)
	}

	allEnriched, err := b.enrichItems(ctx, orderItems)
	if err != nil {
		return nil, fmt.Errorf("enrich order items: %w", err)
	}

	enrichedItemsMap := make(map[uuid.UUID][]ordermodel.OrderItem)
	for _, item := range allEnriched {
		if item.OrderID.Valid {
			enrichedItemsMap[item.OrderID.UUID] = append(enrichedItemsMap[item.OrderID.UUID], item)
		}
	}

	transportMap := lo.KeyBy(transports, func(t orderdb.OrderTransport) int64 { return t.ID })

	result := make([]ordermodel.Order, 0, len(orders))
	for _, o := range orders {
		base := mapOrder(o)
		if t, ok := transportMap[o.TransportID]; ok {
			tr := mapTransport(t)
			base.Transport = &tr
		}
		base.Items = enrichedItemsMap[o.ID]

		confirmSession, err := b.storage.Querier().GetPaymentSession(ctx, uuid.NullUUID{UUID: o.ConfirmSessionID, Valid: true})
		if err != nil {
			return nil, fmt.Errorf("get confirm session: %w", err)
		}
		total, err := b.storage.Querier().SumTotalAmountByOrder(ctx, uuid.NullUUID{UUID: o.ID, Valid: true})
		if err != nil {
			return nil, fmt.Errorf("sum paid amount by order: %w", err)
		}

		base.TotalAmount = total
		confirmMapped := mapPaymentSession(confirmSession)
		base.ConfirmSession = &confirmMapped
		if payoutSession, perr := b.storage.Querier().GetPayoutSessionForOrder(ctx, o.ID); perr == nil {
			payoutMapped := mapPaymentSession(payoutSession)
			base.PayoutSession = &payoutMapped
		}

		result = append(result, base)
	}

	return result, nil
}

type CreditFromSessionParams struct {
	SessionID  uuid.UUID `json:"session_id"`
	AccountID  uuid.UUID `json:"account_id"`
	CreditType string    `json:"credit_type"`
	Reference  string    `json:"reference"`
	Note       string    `json:"note"`
}

// CreditFromSession credits the recipient with the sum of positive-amount Success
// transactions in the given session. Use this when a session is being voided or
// refunded — credits only legs that actually settled, never minting balance for
// unsettled / failed / pending legs. Returns the amount credited; 0 means no-op.
func (b *core) CreditFromSession(
	ctx restate.Context,
	params CreditFromSessionParams,
) (int64, error) {
	settled, err := restate.Run(ctx, func(ctx restate.RunContext) (int64, error) {
		txs, err := b.storage.Querier().ListTransactionsBySession(ctx, params.SessionID)
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
		return 0, err
	}
	if settled == 0 {
		return 0, nil
	}

	if err = b.account.WalletCredit(ctx, accountbiz.WalletCreditParams{
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

type refundCreditReason string

const (
	refundCreditReasonSellerApproved refundCreditReason = "seller-approved"
	refundCreditReasonAutoAccepted   refundCreditReason = "auto-accepted (seller silent)"
	refundCreditReasonAdminDismissed refundCreditReason = "admin-dismissed dispute"
)

// executeRefundCredit performs the actual credit flow: insert refund tx,
// flip refund.status to Accepted, cancel items, credit buyer wallet, signal
// payout workflow. Used by all 3 paths that end in Accepted (seller approve,
// auto-accept timeout, admin dismiss).
func (b *core) executeRefundCredit(
	ctx restate.Context,
	refund orderdb.OrderRefund,
	deciderID uuid.UUID,
	reason refundCreditReason,
) (orderdb.OrderRefund, error) {
	var zero orderdb.OrderRefund

	items, err := restate.Run(ctx, func(rctx restate.RunContext) ([]orderdb.OrderItem, error) {
		return b.storage.Querier().ListItem(rctx, orderdb.ListItemParams{
			OrderID: []uuid.NullUUID{{UUID: refund.OrderID, Valid: true}},
		})
	})
	if err != nil {
		return zero, fmt.Errorf("list items: %w", err)
	}
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

	order, err := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderOrder, error) {
		return b.storage.Querier().GetOrder(rctx, orderdb.GetOrderParams{
			ID: uuid.NullUUID{UUID: refund.OrderID, Valid: true},
		})
	})
	if err != nil {
		return zero, fmt.Errorf("get order: %w", err)
	}

	buyerCurrency, err := b.InferCurrency(ctx, order.BuyerID)
	if err != nil {
		return zero, fmt.Errorf("infer currency: %w", err)
	}

	sessionTxs, err := restate.Run(ctx, func(rctx restate.RunContext) ([]orderdb.OrderTransaction, error) {
		return b.storage.Querier().ListTransactionsBySession(rctx, anyItem.PaymentSessionID)
	})
	if err != nil {
		return zero, fmt.Errorf("list session txs: %w", err)
	}
	originalTx, ok := findOriginalCharge(sessionTxs)
	if !ok {
		return zero, fmt.Errorf("no original tx: %w", ordermodel.ErrOrderItemNotFound)
	}
	originalTxID := uuid.NullUUID{UUID: originalTx.ID, Valid: true}

	refundTxID := restate.UUID(ctx)
	updated, err := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderRefund, error) {
		refundTx, e := b.storage.Querier().CreateDefaultTransaction(rctx, orderdb.CreateDefaultTransactionParams{
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
			u, e = b.storage.Querier().SellerApproveRefund(rctx, orderdb.SellerApproveRefundParams{
				ID:         refund.ID,
				RefundTxID: uuid.NullUUID{UUID: refundTx.ID, Valid: true},
			})
		case orderdb.OrderRefundStatusDisputed:
			u, e = b.storage.Querier().AdminDismissDispute(rctx, orderdb.AdminDismissDisputeParams{
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
			if _, ce := b.storage.Querier().CancelItem(rctx, orderdb.CancelItemParams{
				ID:            it.ID,
				CancelledByID: uuid.NullUUID{UUID: deciderID, Valid: true},
			}); ce != nil {
				return orderdb.OrderRefund{}, fmt.Errorf("cancel item: %w", ce)
			}
		}
		return u, nil
	})
	if err != nil {
		return zero, err
	}

	if _, err := b.CreditFromSession(ctx, CreditFromSessionParams{
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
		if err := b.inventory.ReleaseInventory(ctx, inventorybiz.ReleaseInventoryParams{
			Items: releaseItems,
		}); err != nil {
			return zero, fmt.Errorf("release inventory: %w", err)
		}
	}

	signalPayoutWorkflowOnRefundChanged(ctx, refund.OrderID)
	return updated, nil
}
