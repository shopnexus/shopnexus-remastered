package fullfilment

import (
	"encoding/json"
	"fmt"
	"time"

	restate "github.com/restatedev/sdk-go"

	"shopnexus-server/internal/infras/metrics"
	accountbiz "shopnexus-server/internal/module/account/biz"
	accountmodel "shopnexus-server/internal/module/account/model"
	"shopnexus-server/internal/module/order/biz/workflow/gateway"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/provider/transport"
	"shopnexus-server/internal/shared/saga"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/samber/lo"
)

// confirm drives the seller confirm-fee saga: validate + lock, charge the
// confirm fee (wallet and/or gateway), then atomically create the transport,
// order and item links. The order row is created with ID = workflow key.
func (h *FulfillmentWorkflow) confirm(
	ctx restate.WorkflowContext,
	sg *saga.Saga,
	input FulfillmentInput,
	orderID uuid.UUID,
) (res confirmResult, err error) {
	sessionID := orderID // confirm session ID = order ID = workflow key
	sellerID := input.Account.ID

	// Lock seller pending so two concurrent confirms over an overlapping
	// ItemIDs slice can't double-spend wallet balance or skip validation.
	// Scoped to the confirm phase — the escrow phase lives for days and must
	// not hold it.
	unlock := h.locker.Lock(ctx, fmt.Sprintf("order:seller-pending:%s", sellerID))
	defer unlock()

	orderItems, err := h.fetchItems(ctx, input.ItemIDs)
	if err != nil {
		return res, err
	}

	buyerID, address, transportOption, paidTotal, paymentSessionIDs, err := aggregateItems(orderItems, sellerID)
	if err != nil {
		return res, err
	}

	// Confirming items the buyer never paid for would mint inventory for free.
	for psID := range paymentSessionIDs {
		status, sErr := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderStatus, error) {
			session, e := h.Storage.Querier().GetPaymentSession(rctx, uuid.NullUUID{UUID: psID, Valid: true})
			if e != nil {
				return "", fmt.Errorf("get payment session: %w", e)
			}
			return session.Status, nil
		})
		if sErr != nil {
			return res, fmt.Errorf("check payment session status: %w", sErr)
		}
		if status != orderdb.OrderStatusSuccess {
			return res, ordermodel.ErrPaymentNotSuccess
		}
	}

	// One quote covers all items — they share transport_option and address.
	contactMap, err := h.account.GetDefaultContact(ctx, []uuid.UUID{sellerID})
	if err != nil {
		return res, fmt.Errorf("get seller contact: %w", err)
	}
	transportClient, err := h.GetTransportClient(transportOption)
	if err != nil {
		return res, fmt.Errorf("get transport client: %w", err)
	}
	transportItems := lo.Map(orderItems, func(item orderdb.OrderItem, _ int) transport.ItemMetadata {
		return transport.ItemMetadata{SkuID: item.SkuID, Quantity: item.Quantity}
	})
	quote, err := transportClient.Quote(ctx, transport.QuoteParams{
		Items:       transportItems,
		FromAddress: contactMap[sellerID].Address,
		ToAddress:   address,
	})
	if err != nil {
		return res, fmt.Errorf("quote transport: %w", err)
	}

	platformFee := int64(0) // TODO: plug config
	confirmFeeTotal := quote.Cost + platformFee

	// Confirm-fee txs are denominated in the seller's currency (the seller is
	// paying the platform). InferCurrency is cross-module → outside Run.
	sellerCurrency, err := h.InferCurrency(ctx, sellerID)
	if err != nil {
		return res, fmt.Errorf("infer seller currency: %w", err)
	}

	confirmFeeWallet, confirmFeeGateway, err := h.splitConfirmFee(ctx, input, sellerID, confirmFeeTotal)
	if err != nil {
		return res, err
	}
	if confirmFeeGateway > 0 && input.PaymentOption == "" {
		return res, ordermodel.ErrInsufficientWalletBalance
	}

	// Pre-allocate the wallet tx UUID via restate.UUID so retries reuse the
	// same value and the INSERT is idempotent on PK conflict. Order / transport
	// / item-link rows are deferred to the post-paid Run below, so they need no
	// compensation on rollback. Gateway txs are minted per attempt in the loop,
	// so the compensator marks every still-Pending child tx by session_id.
	walletTxID := restate.UUID(ctx)

	sg.Defer("mark_session_and_txs_failed", func(ctx restate.Context) error {
		return restate.RunVoid(ctx, func(rctx restate.RunContext) error {
			return gateway.MarkSessionFailed(rctx, h.Storage.Querier(), sessionID, "confirm saga compensation")
		})
	})

	if err = restate.RunVoid(ctx, func(rctx restate.RunContext) error {
		if _, sErr := h.Storage.Querier().CreateDefaultPaymentSession(rctx, orderdb.CreateDefaultPaymentSessionParams{
			ID:          sessionID,
			Kind:        ordermodel.SessionKindSellerConfirmationFee,
			Status:      orderdb.OrderStatusPending,
			FromID:      uuid.NullUUID{UUID: sellerID, Valid: true},
			ToID:        uuid.NullUUID{},
			Note:        "seller confirmation fee",
			Currency:    sellerCurrency,
			TotalAmount: confirmFeeTotal,
			Data:        json.RawMessage("{}"),
			DatePaid:    null.Time{},
			DateExpired: time.Now().Add(gateway.SessionExpiry),
		}); sErr != nil {
			return fmt.Errorf("db create confirm session: %w", sErr)
		}
		if confirmFeeWallet > 0 {
			if _, txErr := h.Storage.Querier().CreateDefaultTransaction(rctx, orderdb.CreateDefaultTransactionParams{
				ID:        walletTxID,
				SessionID: sessionID,
				Status:    orderdb.OrderStatusPending,
				Note:      "confirm fee wallet payment",
				Data:      json.RawMessage("{}"),
				Amount:    confirmFeeWallet,
				Currency:  sellerCurrency,
			}); txErr != nil {
				return fmt.Errorf("db create confirm_fee wallet tx: %w", txErr)
			}
		}
		return nil
	}); err != nil {
		return res, fmt.Errorf("create confirm fee session and txs: %w", err)
	}

	if confirmFeeWallet > 0 {
		if _, dErr := h.account.Call().WalletDebit(ctx, accountbiz.WalletDebitParams{
			AccountID: sellerID,
			Amount:    confirmFeeWallet,
			Reference: fmt.Sprintf("tx:%s", walletTxID),
			Note:      "confirm fee wallet payment",
		}); dErr != nil {
			return res, fmt.Errorf("seller wallet debit: %w", dErr)
		}
		// Arm credit compensator AFTER debit confirmed. WalletDebit is atomic
		// (single CTE under FOR UPDATE) → terminal failure means no debit, so
		// arming earlier would over-credit on saga fire.
		sg.Defer("credit_wallet", func(ctx restate.Context) error {
			return h.account.Call().WalletCredit(ctx, accountbiz.WalletCreditParams{
				AccountID: sellerID,
				Amount:    confirmFeeWallet,
				Type:      "Refund",
				Reference: fmt.Sprintf("tx:%s", walletTxID),
				Note:      "saga compensate: confirm fee wallet debit",
			})
		})
		if err = restate.RunVoid(ctx, func(rctx restate.RunContext) error {
			_, e := h.Storage.Querier().MarkTransactionSuccess(rctx, orderdb.MarkTransactionSuccessParams{
				ID:          walletTxID,
				DateSettled: time.Now(),
			})
			return e
		}); err != nil {
			return res, fmt.Errorf("mark confirm-fee wallet tx success: %w", err)
		}
	}

	if confirmFeeGateway > 0 {
		if err = h.gw.RunPaymentLoop(ctx, gateway.LoopParams{
			SessionID:       sessionID,
			SessionDeadline: time.Now().Add(gateway.SessionExpiry),
			NotePrefix:      "confirm fee gateway payment",
			Description:     fmt.Sprintf("Confirm fee session %s", sessionID),
			PaymentOption:   input.PaymentOption,
			Amount:          confirmFeeGateway,
			Currency:        sellerCurrency,
			ErrCancelled:    ordermodel.ErrConfirmCancelled,
			ErrExpired:      ordermodel.ErrConfirmExpired,
		}); err != nil {
			return res, fmt.Errorf("gateway payment loop: %w", err)
		}
	}

	// Paid path — clear the saga and atomically create transport, order
	// (ID = workflow key) and item links. The forward (seller→buyer) leg is
	// created Pending; the real OnTransportResult webhook flips it Success.
	// Failure here is a programmer bug (the seller already paid us) → terminal.
	sg.Clear()

	if err = restate.RunVoid(ctx, func(rctx restate.RunContext) error {
		quoteData, _ := json.Marshal(map[string]int64{"quote": quote.Cost})
		trRow, tErr := h.Storage.Querier().CreateDefaultTransport(rctx, orderdb.CreateDefaultTransportParams{
			Option: transportOption,
			Data:   json.RawMessage(quoteData),
		})
		if tErr != nil {
			return fmt.Errorf("db create transport: %w", tErr)
		}
		if oErr := h.Storage.Querier().CreateOrderIdempotent(rctx, orderdb.CreateOrderIdempotentParams{
			ID:               orderID,
			BuyerID:          buyerID,
			SellerID:         sellerID,
			TransportID:      trRow.ID,
			Address:          address,
			DateCreated:      time.Now(),
			ConfirmedByID:    input.Account.ID,
			ConfirmSessionID: sessionID,
			Note:             null.NewString(input.Note, input.Note != ""),
		}); oErr != nil {
			return fmt.Errorf("db create order: %w", oErr)
		}
		if lErr := h.Storage.Querier().SetItemsOrderID(rctx, orderdb.SetItemsOrderIDParams{
			OrderID: uuid.NullUUID{UUID: orderID, Valid: true},
			ItemIds: input.ItemIDs,
		}); lErr != nil {
			return fmt.Errorf("db set items order id: %w", lErr)
		}
		return nil
	}); err != nil {
		return res, fmt.Errorf("create order records: %w", err)
	}

	metrics.OrdersCreatedTotal.Inc()

	// Wallet-only confirms never enter the gateway loop — resolve the first
	// payment-URL promise with an empty URL so the synchronous HTTP caller
	// short-circuits instead of hanging.
	if confirmFeeGateway == 0 {
		_ = restate.Promise[string](ctx, "payment_url_1").Resolve("")
	}

	itemNames := lo.Map(orderItems, func(it orderdb.OrderItem, _ int) string { return it.SkuName })
	if err = h.NotifyOrder(ctx, buyerID, orderID, accountmodel.NotiItemsConfirmed, "Items confirmed",
		fmt.Sprintf("%s has been confirmed by the seller.", ordermodel.SummarizeNames(itemNames))); err != nil {
		return res, fmt.Errorf("notify items confirmed: %w", err)
	}

	return confirmResult{SellerID: sellerID, PaidTotal: paidTotal, Currency: sellerCurrency}, nil
}

// fetchItems loads the items inside a Run so list ordering and missing-row
// checks are journaled (replay returns the exact same slice).
func (h *FulfillmentWorkflow) fetchItems(ctx restate.WorkflowContext, itemIDs []int64) ([]orderdb.OrderItem, error) {
	items, err := restate.Run(ctx, func(rctx restate.RunContext) ([]orderdb.OrderItem, error) {
		res, e := h.Storage.Querier().ListItem(rctx, orderdb.ListItemParams{Id: itemIDs})
		if e != nil {
			return nil, fmt.Errorf("db list items: %w", e)
		}
		if len(res.Data) != len(itemIDs) {
			return nil, ordermodel.ErrOrderItemNotFound
		}
		return res.Data, nil
	})
	if err != nil {
		return nil, fmt.Errorf("fetch items: %w", err)
	}
	return items, nil
}

// aggregateItems validates cross-item invariants and sums the shared fields.
// Every item must be owned by this seller, share buyer/address/transport, and
// not already be final. Validation is deterministic over the journaled slice,
// so any failure is terminal — otherwise Restate retries the same invariant
// violation forever. fmt.Errorf("%w") strips the marker on ordermodel errors,
// so failures re-wrap with restate.TerminalError. paidTotal is the eventual
// escrow payout amount.
func aggregateItems(items []orderdb.OrderItem, sellerID uuid.UUID) (
	buyerID uuid.UUID,
	address, transportOption string,
	paidTotal int64,
	paymentSessionIDs map[uuid.UUID]struct{},
	err error,
) {
	paymentSessionIDs = make(map[uuid.UUID]struct{})
	for i, item := range items {
		switch {
		case item.OrderID.Valid:
			return buyerID, address, transportOption, 0, nil,
				restate.TerminalError(fmt.Errorf("item %d: %w", item.ID, ordermodel.ErrItemAlreadyConfirmed))
		case item.DateCancelled.Valid:
			return buyerID, address, transportOption, 0, nil,
				restate.TerminalError(fmt.Errorf("item %d: %w", item.ID, ordermodel.ErrItemAlreadyCancelled))
		case item.SellerID != sellerID:
			return buyerID, address, transportOption, 0, nil,
				restate.TerminalError(fmt.Errorf("item %d: %w", item.ID, ordermodel.ErrItemNotOwnedBySeller))
		}
		if i == 0 {
			buyerID, address, transportOption = item.AccountID, item.Address, item.TransportOption
		} else {
			switch {
			case item.AccountID != buyerID:
				return buyerID, address, transportOption, 0, nil,
					restate.TerminalError(fmt.Errorf("item %d: %w", item.ID, ordermodel.ErrItemsNotSameBuyer))
			case item.Address != address:
				return buyerID, address, transportOption, 0, nil,
					restate.TerminalError(fmt.Errorf("item %d: %w", item.ID, ordermodel.ErrItemsNotSameAddress))
			case item.TransportOption != transportOption:
				return buyerID, address, transportOption, 0, nil,
					restate.TerminalError(fmt.Errorf("item %d: %w", item.ID, ordermodel.ErrItemsTransportMismatch))
			}
		}
		paidTotal += item.TotalAmount
		paymentSessionIDs[item.PaymentSessionID] = struct{}{}
	}
	return buyerID, address, transportOption, paidTotal, paymentSessionIDs, nil
}

// splitConfirmFee decides how much of the confirm fee is paid from the seller's
// wallet versus the gateway. Wallet covers as much as its balance allows; the
// remainder goes to the gateway.
func (h *FulfillmentWorkflow) splitConfirmFee(
	ctx restate.WorkflowContext,
	input FulfillmentInput,
	sellerID uuid.UUID,
	total int64,
) (fromWallet, fromGateway int64, err error) {
	if input.UseWallet && total > 0 {
		balance, balErr := h.account.GetWalletBalance(ctx, sellerID)
		if balErr != nil {
			return 0, 0, fmt.Errorf("get seller wallet balance: %w", balErr)
		}
		fromWallet = min(balance, total)
	}
	return fromWallet, total - fromWallet, nil
}
