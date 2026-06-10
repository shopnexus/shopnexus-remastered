package checkout

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	restate "github.com/restatedev/sdk-go"

	"shopnexus-server/internal/infras/metrics"
	accountbiz "shopnexus-server/internal/module/account/biz"
	accountmodel "shopnexus-server/internal/module/account/model"
	analyticbiz "shopnexus-server/internal/module/analytic/biz"
	analyticmodel "shopnexus-server/internal/module/analytic/model"
	catalogbiz "shopnexus-server/internal/module/catalog/biz"
	catalogmodel "shopnexus-server/internal/module/catalog/model"
	commonbiz "shopnexus-server/internal/module/common/biz"
	commonmodel "shopnexus-server/internal/module/common/model"
	inventorybiz "shopnexus-server/internal/module/inventory/biz"
	inventorydb "shopnexus-server/internal/module/inventory/db/sqlc"
	orderbase "shopnexus-server/internal/module/order/biz/base"
	"shopnexus-server/internal/module/order/biz/workflow/base"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/provider/transport"
	sharedcurrency "shopnexus-server/internal/shared/currency"
	"shopnexus-server/internal/shared/idempotency"
	"shopnexus-server/internal/shared/saga"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/samber/lo"
)

// CheckoutWorkflow drives the buyer checkout saga. It embeds the module core
// (*orderbase.Base) for the shared storage/notify/hydrate helpers and holds the
// workflow core (the gateway payment loop) as a named field.
type CheckoutWorkflow struct {
	*orderbase.Base

	wf        *base.Base
	account   accountbiz.AccountBizClient
	catalog   catalogbiz.CatalogBizClient
	common    commonbiz.CommonBizClient
	inventory inventorybiz.InventoryBizClient
}

func New(
	core *orderbase.Base,
	wf *base.Base,
	account accountbiz.AccountBizClient,
	catalog catalogbiz.CatalogBizClient,
	common commonbiz.CommonBizClient,
	inventory inventorybiz.InventoryBizClient,
) *CheckoutWorkflow {
	return &CheckoutWorkflow{core, wf, account, catalog, common, inventory}
}

func (h *CheckoutWorkflow) ServiceName() string { return "CheckoutWorkflow" }

func (h *CheckoutWorkflow) Run(
	ctx restate.WorkflowContext,
	input CheckoutWorkflowInput,
) (out CheckoutWorkflowOutput, err error) {
	defer metrics.TrackHandler("checkout_workflow", "Run", &err)()

	workflowID := uuid.MustParse(restate.Key(ctx))
	sessionID := workflowID

	// Step 1: Validate.
	if err = validator.Validate(input); err != nil {
		return out, fmt.Errorf("validate checkout: %w", err)
	}
	if input.BuyNow && len(input.Items) != 1 {
		return out, ordermodel.ErrBuyNowSingleSkuOnly
	}

	saga := saga.New(ctx)
	defer func() {
		if restate.IsTerminalError(err) {
			saga.Compensate()
		}
	}()

	// Reject WaitPaymentURL on terminal failure so the synchronous HTTP
	saga.Defer("reject_payment_url", func(_ restate.Context) error {
		return restate.Promise[string](ctx, "payment_url_1").Reject(err)
	})

	// Step 1.5: Load buyer profile and enforce address country match.
	buyerProfile, err := h.account.GetProfile(ctx, accountbiz.GetProfileParams{
		AccountID: input.Account.ID,
	})
	if err != nil {
		return out, fmt.Errorf("load buyer profile: %w", err)
	}

	resolvedCountry, err := h.common.ResolveCountry(ctx, input.Address)
	if err != nil {
		return out, fmt.Errorf("resolve country: %w", err)
	}
	if resolvedCountry != buyerProfile.Country {
		return out, ordermodel.ErrCheckoutAddressCountryMismatch.Fmt(resolvedCountry, buyerProfile.Country)
	}

	skuIDs := lo.Map(input.Items, func(s CheckoutItem, _ int) uuid.UUID { return s.SkuID })
	checkoutItemMap := lo.KeyBy(input.Items, func(s CheckoutItem) uuid.UUID { return s.SkuID })

	// Step 2: Fetch product data (SKUs + SPUs).
	skus, err := h.catalog.ListProductSku(ctx, catalogbiz.ListProductSkuParams{
		ID: skuIDs,
	})
	if err != nil {
		return out, fmt.Errorf("fetch product skus: %w", err)
	}
	if len(skus) != len(skuIDs) {
		return out, ordermodel.ErrOrderItemNotFound
	}

	listSpu, err := h.catalog.ListProductSpu(ctx, catalogbiz.ListProductSpuParams{
		Account: input.Account,
		ID:      lo.Map(skus, func(s catalogmodel.ProductSku, _ int) uuid.UUID { return s.SpuID }),
	})
	if err != nil {
		return out, fmt.Errorf("fetch product spus: %w", err)
	}

	skuMap := lo.KeyBy(skus, func(s catalogmodel.ProductSku) uuid.UUID { return s.ID })
	spuMap := lo.KeyBy(listSpu.Data, func(s catalogmodel.ProductSpu) uuid.UUID { return s.ID })

	// Step 2.5: FX snapshot. Mixed-currency carts are supported — each item
	// converts from its spu's own currency into the buyer's currency.
	if len(listSpu.Data) == 0 {
		return out, ordermodel.ErrOrderItemNotFound
	}

	buyerCurrency, err := sharedcurrency.Infer(buyerProfile.Country)
	if err != nil {
		return out, fmt.Errorf("infer buyer currency: %w", err)
	}

	needFX := false
	for _, spu := range listSpu.Data {
		if spu.Currency != buyerCurrency {
			needFX = true
			break
		}
	}

	var fxSnapshot commonmodel.ExchangeRateSnapshot
	if needFX {
		fxSnapshot, err = h.common.GetExchangeRates(ctx, commonbiz.GetExchangeRatesParams{})
		if err != nil {
			return out, fmt.Errorf("fx rate lookup: %w", err)
		}
		// Validate rate availability up-front for every currency we'll touch
		// (buyer + each unique non-buyer seller currency). Fails loud before
		// inventory reservation, since ConvertAmountPure is fail-open.
		needed := map[string]struct{}{buyerCurrency: {}}
		for _, spu := range listSpu.Data {
			needed[spu.Currency] = struct{}{}
		}
		for c := range needed {
			if c == fxSnapshot.Base {
				continue
			}
			if r, ok := fxSnapshot.Rates[c]; !ok || r.Sign() <= 0 {
				return out, ordermodel.ErrFXRateUnavailable.Fmt(c)
			}
		}
	}

	// Session/tx settle in buyer currency. Per-item conversion is baked into
	// the stored subtotal/total amounts; the FX snapshot lives on the session
	// (one source of truth across all child txs) so audit can replay any
	// per-item conversion via (item.source_currency, session.fx_snapshot).
	var fxSnapshotJSON json.RawMessage
	if needFX {
		raw, mErr := json.Marshal(fxSnapshot)
		if mErr != nil {
			return out, fmt.Errorf("marshal fx snapshot: %w", mErr)
		}
		fxSnapshotJSON = raw
	}

	convertToBuyer := func(amount int64, fromCurrency string) int64 {
		if fromCurrency == buyerCurrency {
			return amount
		}
		return commonbiz.ConvertAmountPure(amount, fromCurrency, buyerCurrency, fxSnapshot.Rates)
	}

	// Step 3: Remove items from cart (skip on BuyNow)
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
		if err = restate.RunVoid(ctx, func(rctx restate.RunContext) error {
			_, e := h.Storage.Querier().RemoveCheckoutItem(rctx, orderdb.RemoveCheckoutItemParams{
				AccountID: input.Account.ID,
				SkuID:     skuIDs,
			})
			return e
		}); err != nil {
			return out, fmt.Errorf("remove cart items: %w", err)
		}
	}

	// Step 4: Reserve inventory.
	// Saga key paired across forward (Reserve, claims) and compensator (Release, consumes)
	// so a failure or partial commit unwinds without double-incrementing stock.
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
			if sku, ok := skuMap[item.SkuID]; ok {
				if spu, ok := spuMap[sku.SpuID]; ok {
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
		return out, fmt.Errorf("reserve inventory: %w", err)
	}

	serialIDsMap := lo.SliceToMap(inventories, func(i inventorybiz.ReserveInventoryResult) (uuid.UUID, []string) {
		return i.RefID, i.SerialIDs
	})

	// Step 5: Quote transport per item.
	sellerIDs := lo.Uniq(lo.Map(skus, func(s catalogmodel.ProductSku, _ int) uuid.UUID {
		return spuMap[s.SpuID].AccountID
	}))

	sellerContacts, err := h.account.GetDefaultContact(ctx, sellerIDs)
	if err != nil {
		return out, fmt.Errorf("fetch seller contacts: %w", err)
	}

	type transportQuote struct {
		Option string `json:"option"`
		Cost   int64  `json:"cost"`
	}
	transportQuotes := make(map[uuid.UUID]transportQuote)

	for _, item := range input.Items {
		sku := skuMap[item.SkuID]
		spu := spuMap[sku.SpuID]

		transportClient, tcErr := h.GetTransportClient(item.TransportOption)
		if tcErr != nil {
			return out, fmt.Errorf("get transport client: %w", tcErr)
		}

		sellerContact, ok := sellerContacts[spu.AccountID]
		if !ok {
			return out, fmt.Errorf("seller contact not found: %w", ordermodel.ErrOrderItemNotFound)
		}

		quote, qErr := transportClient.Quote(ctx, transport.QuoteParams{
			Items: []transport.ItemMetadata{{
				SkuID:    item.SkuID,
				Quantity: item.Quantity,
			}},
			FromAddress: sellerContact.Address,
			ToAddress:   input.Address,
		})
		if qErr != nil {
			return out, fmt.Errorf("quote transport for sku %s: %w", item.SkuID, qErr)
		}

		transportQuotes[item.SkuID] = transportQuote{
			Option: item.TransportOption,
			Cost:   quote.Cost,
		}
	}

	// Step 6: Compute totals.
	type itemAmounts struct {
		subtotalAmount int64
		totalAmount    int64
	}
	itemAmountsMap := make(map[uuid.UUID]itemAmounts)
	var total int64
	for _, item := range input.Items {
		sku := skuMap[item.SkuID]
		spu := spuMap[sku.SpuID]
		tq := transportQuotes[item.SkuID]
		subtotal := convertToBuyer(sku.Price*item.Quantity, spu.Currency)
		paid := subtotal + convertToBuyer(tq.Cost, spu.Currency)
		itemAmountsMap[item.SkuID] = itemAmounts{subtotalAmount: subtotal, totalAmount: paid}
		total += paid
	}

	// Step 7: Wallet / gateway split.
	var internalWalletAmount, gatewayAmount int64
	if input.UseWallet && total > 0 {
		balance, balErr := h.account.GetWalletBalance(ctx, input.Account.ID)
		if balErr != nil {
			return out, fmt.Errorf("get wallet balance: %w", balErr)
		}
		internalWalletAmount = min(balance, total)
	}
	gatewayAmount = total - internalWalletAmount

	if gatewayAmount > 0 && input.PaymentOption == "" {
		return out, ordermodel.ErrInsufficientWalletBalance
	}

	// Step 8: Atomically create payment_session, the wallet tx (if any),
	// and order items in a single restate.Run. Gateway txs are NOT created
	// here — they're inserted per-attempt inside the retry loop below, so
	// each retry mints a fresh tx with its own gateway URL.
	internalWalletTxID := restate.UUID(ctx)

	// Compensator: mark payment_session + every still-Pending child tx as
	// Failed by session_id. Multi-attempt sessions can spawn N gateway txs
	// across the loop, so the compensator can't track IDs explicitly —
	// session-wide marking catches them all idempotently.
	saga.Defer("mark_session_and_txs_failed", func(ctx restate.Context) error {
		return restate.RunVoid(ctx, func(rctx restate.RunContext) error {
			return base.MarkSessionAndAllPendingFailed(
				rctx,
				h.Storage.Querier(),
				sessionID,
				"checkout saga compensation",
			)
		})
	})

	// Result fields aren't needed downstream (the workflow drives the rest
	// from input + pre-allocated UUIDs), but keeping them in the journal
	// gives us a clean replay trace for debugging.
	type checkoutRunResult struct {
		Session orderdb.OrderPaymentSession `json:"session"`
		Items   []orderdb.OrderItem         `json:"items"`
	}
	_, err = restate.Run(ctx, func(rctx restate.RunContext) (checkoutRunResult, error) {
		var res checkoutRunResult

		session, sErr := h.Storage.Querier().
			CreateDefaultPaymentSession(rctx, orderdb.CreateDefaultPaymentSessionParams{
				ID:          sessionID,
				Kind:        ordermodel.SessionKindBuyerCheckout,
				Status:      orderdb.OrderStatusPending,
				FromID:      uuid.NullUUID{UUID: input.Account.ID, Valid: true},
				ToID:        uuid.NullUUID{},
				Note:        "buyer checkout",
				Currency:    buyerCurrency,
				TotalAmount: total,
				FxSnapshot:  fxSnapshotJSON,
				Data:        json.RawMessage("{}"),
				DatePaid:    null.Time{},
				DateExpired: time.Now().Add(base.SessionExpiry),
			})
		if sErr != nil {
			return res, fmt.Errorf("db create payment session: %w", sErr)
		}
		res.Session = session

		if internalWalletAmount > 0 {
			if _, txErr := h.Storage.Querier().CreateDefaultTransaction(rctx, orderdb.CreateDefaultTransactionParams{
				ID:            internalWalletTxID,
				SessionID:     sessionID,
				Status:        orderdb.OrderStatusPending,
				Note:          "checkout wallet payment",
				Error:         null.String{},
				PaymentOption: null.String{},
				Data:          json.RawMessage("{}"),
				Amount:        internalWalletAmount,
				Currency:      buyerCurrency,
				ReversesID:    uuid.NullUUID{},
				DateSettled:   null.Time{},
				DateExpired:   null.Time{},
			}); txErr != nil {
				return res, fmt.Errorf("db create wallet tx: %w", txErr)
			}
		}

		// Gateway txs are minted per-attempt below, not here.

		for _, checkoutItem := range input.Items {
			sku := skuMap[checkoutItem.SkuID]
			spu := spuMap[sku.SpuID]
			serialIDs := serialIDsMap[checkoutItem.SkuID]
			amounts := itemAmountsMap[checkoutItem.SkuID]
			tq := transportQuotes[checkoutItem.SkuID]

			jsonSerialIDs, mErr := json.Marshal(serialIDs)
			if mErr != nil {
				return res, fmt.Errorf("marshal serial ids: %w", mErr)
			}

			skuName := spu.Name
			if len(sku.Attributes) > 0 {
				vals := make([]string, 0, len(sku.Attributes))
				for _, attr := range sku.Attributes {
					vals = append(vals, attr.Value)
				}
				skuName += " - " + strings.Join(vals, " / ")
			}

			dbItem, iErr := h.Storage.Querier().CreateDefaultItem(rctx, orderdb.CreateDefaultItemParams{
				OrderID:          uuid.NullUUID{},
				AccountID:        input.Account.ID,
				SellerID:         spu.AccountID,
				SkuID:            sku.ID,
				SpuID:            sku.SpuID,
				SkuName:          skuName,
				Address:          input.Address,
				Note:             null.NewString(checkoutItem.Note, checkoutItem.Note != ""),
				SerialIds:        jsonSerialIDs,
				Quantity:         checkoutItem.Quantity,
				TransportOption:  tq.Option,
				SubtotalAmount:   amounts.subtotalAmount,
				TotalAmount:      amounts.totalAmount,
				SourceCurrency:   spu.Currency,
				PaymentSessionID: sessionID,
				DateCancelled:    null.Time{},
				CancelledByID:    uuid.NullUUID{},
			})
			if iErr != nil {
				return res, fmt.Errorf("db create item: %w", iErr)
			}
			res.Items = append(res.Items, dbItem)
		}

		return res, nil
	})
	if err != nil {
		metrics.CheckoutItemsCreatedTotal.WithLabelValues("failure").Inc()
		return out, fmt.Errorf("create checkout records: %w", err)
	}

	// Step 9: Internal wallet payment. The wallet tx was created Pending in
	// step 8; we mark it Success after the debit acknowledges.
	if internalWalletAmount > 0 {
		if _, dErr := h.account.Call().WalletDebit(ctx, accountbiz.WalletDebitParams{
			AccountID: input.Account.ID,
			Amount:    internalWalletAmount,
			Reference: fmt.Sprintf("tx:%s", internalWalletTxID),
			Note:      "checkout internal wallet",
		}); dErr != nil {
			return out, fmt.Errorf("debit internal wallet: %w", dErr)
		}
		// Arm credit compensator AFTER debit confirmed. WalletDebit is atomic
		// (single CTE under FOR UPDATE) → terminal failure means no debit
		// happened, so registering before would over-credit on saga fire.
		// TODO: xem lại step này, vì đang ko đc hỗ trợ idempotency => có thể double credit/debit
		saga.Defer("credit_internal_wallet", func(ctx restate.Context) error {
			return h.account.Call().WalletCredit(ctx, accountbiz.WalletCreditParams{
				AccountID: input.Account.ID,
				Amount:    internalWalletAmount,
				Type:      "Refund",
				Reference: fmt.Sprintf("tx:%s", internalWalletTxID),
				Note:      "saga compensate: checkout wallet debit",
			})
		})
		if mErr := restate.RunVoid(ctx, func(rctx restate.RunContext) error {
			_, e := h.Storage.Querier().MarkTransactionSuccess(rctx, orderdb.MarkTransactionSuccessParams{
				ID:          internalWalletTxID,
				DateSettled: time.Now(),
			})
			return e
		}); mErr != nil {
			return out, fmt.Errorf("mark wallet tx success: %w", mErr)
		}
	}

	// Step 10–11: Gateway payment loop (skipped for wallet-only). Mints a
	// fresh gateway tx per attempt, resolves payment_url_<attempt>, lazy-
	// retries on attempt expiry. Shared with FulfillmentWorkflow via the
	// workflow base.
	if gatewayAmount > 0 {
		if err = h.wf.RunGatewayPaymentLoop(ctx, base.GatewayPaymentLoopParams{
			SessionID:       sessionID,
			WorkflowID:      workflowID,
			SessionDeadline: time.Now().Add(base.SessionExpiry),
			NotePrefix:      "checkout gateway payment",
			Description:     fmt.Sprintf("Checkout session %s", workflowID),
			PaymentOption:   input.PaymentOption,
			Amount:          gatewayAmount,
			Currency:        buyerCurrency,
			ErrCancelled:    ordermodel.ErrCheckoutCancelled,
			ErrExpired:      ordermodel.ErrCheckoutExpired,
		}); err != nil {
			return out, fmt.Errorf("gateway payment loop: %w", err)
		}
	}

	// Step 12: Success tail — clear saga, fan out side effects.
	saga.Clear()

	purchaseInteractions := make([]analyticbiz.CreateInteraction, 0, len(input.Items))
	for _, item := range input.Items {
		purchaseInteractions = append(purchaseInteractions, analyticbiz.CreateInteraction{
			Account:   input.Account,
			EventType: analyticmodel.EventPurchase,
			RefType:   analyticmodel.InteractionRefTypeProduct,
			RefID:     item.SkuID.String(),
		})
	}
	if err = h.TrackInteractions(ctx, purchaseInteractions...); err != nil {
		return out, fmt.Errorf("track purchase interactions: %w", err)
	}

	sellerItems := make(map[uuid.UUID][]string)
	for _, item := range input.Items {
		sku := skuMap[item.SkuID]
		spu := spuMap[sku.SpuID]
		sellerItems[spu.AccountID] = append(sellerItems[spu.AccountID], spu.Name)
	}
	for sellerID, names := range sellerItems {
		summary := ordermodel.SummarizeNames(names)
		if err = h.Notify(ctx, accountbiz.CreateNotificationParams{
			AccountID: sellerID,
			Type:      accountmodel.NotiNewPendingItems,
			Channel:   accountmodel.ChannelInApp,
			Title:     "New pending items",
			Content:   fmt.Sprintf("New order for %s is waiting for your review.", summary),
		}); err != nil {
			return out, fmt.Errorf("notify seller: %w", err)
		}
	}

	return CheckoutWorkflowOutput{Status: "paid", SessionID: sessionID}, nil
}
