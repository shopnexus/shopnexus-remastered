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
	ordermodel "shopnexus-server/internal/module/order/model"
	orderrepo "shopnexus-server/internal/module/order/repo"
	"shopnexus-server/internal/provider/transport"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/samber/lo"
)

// confirm drives the seller confirm-fee saga: lock, charge, then atomically create transport + order + item links.
func (r *fulfillmentRun) confirm() (err error) {
	ctx := r.ctx
	sessionID := r.orderID // confirm session ID = order ID = workflow key
	sellerID := r.input.Account.ID

	orderItems, err := r.fetchItems(ctx, r.input.ItemIDs)
	if err != nil {
		return err
	}

	buyerID, address, transportOption, paidTotal, paymentSessionIDs, err := aggregateItems(orderItems, sellerID)
	if err != nil {
		return err
	}

	// Step 2: require every buyer payment session to be already paid
	for psID := range paymentSessionIDs {
		status, sErr := restate.Run(ctx, func(rctx restate.RunContext) (ordermodel.Status, error) {
			session, e := r.Storage.Querier().GetPaymentSession(rctx, psID)
			if e != nil {
				return "", fmt.Errorf("get payment session: %w", e)
			}
			return session.Status, nil
		})
		if sErr != nil {
			return fmt.Errorf("check payment session status: %w", sErr)
		}
		if status != ordermodel.StatusSuccess {
			return ordermodel.ErrPaymentNotSuccess
		}
	}

	// Step 3: quote transport + split the confirm fee into wallet/gateway legs
	contactMap, err := r.account.GetDefaultContact(ctx, []uuid.UUID{sellerID})
	if err != nil {
		return fmt.Errorf("get seller contact: %w", err)
	}
	transportClient, err := r.GetTransportClient(transportOption)
	if err != nil {
		return fmt.Errorf("get transport client: %w", err)
	}
	transportItems := lo.Map(orderItems, func(item ordermodel.OrderItem, _ int) transport.ItemMetadata {
		return transport.ItemMetadata{SkuID: item.SkuID, Quantity: item.Quantity}
	})
	quote, err := transportClient.Quote(ctx, transport.QuoteParams{
		Items:       transportItems,
		FromAddress: contactMap[sellerID].Address,
		ToAddress:   address,
	})
	if err != nil {
		return fmt.Errorf("quote transport: %w", err)
	}

	platformFee := int64(0) // TODO: plug config
	confirmFeeTotal := quote.Cost + platformFee

	sellerCurrency, err := r.InferCurrency(ctx, sellerID)
	if err != nil {
		return fmt.Errorf("infer seller currency: %w", err)
	}

	confirmFeeWallet, confirmFeeGateway, err := r.splitConfirmFee(ctx, r.input, sellerID, confirmFeeTotal)
	if err != nil {
		return err
	}
	if confirmFeeGateway > 0 && r.input.PaymentOption == "" {
		return ordermodel.ErrInsufficientWalletBalance
	}

	// Step 4: create confirm-fee session + wallet tx in one journaled tx
	// restate.UUID journals the value so retries reuse it — INSERT is idempotent on PK conflict.
	walletTxID := restate.UUID(ctx)

	r.sg.Defer("mark_session_and_txs_failed", func(ctx restate.Context) error {
		return restate.RunVoid(ctx, func(rctx restate.RunContext) error {
			return gateway.MarkSessionFailed(rctx, r.Storage.Querier(), sessionID, "confirm saga compensation")
		})
	})

	if err = restate.RunVoid(ctx, func(rctx restate.RunContext) error {
		if _, sErr := r.Storage.Querier().CreatePaymentSession(rctx, orderrepo.CreatePaymentSessionParams{
			ID:          sessionID,
			Kind:        ordermodel.SessionKindSellerConfirmationFee,
			Status:      ordermodel.StatusPending,
			FromID:      uuid.NullUUID{UUID: sellerID, Valid: true},
			Note:        "seller confirmation fee",
			Currency:    sellerCurrency,
			TotalAmount: confirmFeeTotal,
			Data:        json.RawMessage("{}"),
			DateExpired: time.Now().Add(gateway.SessionExpiry),
		}); sErr != nil {
			return fmt.Errorf("db create confirm session: %w", sErr)
		}
		if confirmFeeWallet > 0 {
			if _, txErr := r.Storage.Querier().CreateTransaction(rctx, orderrepo.CreateTransactionParams{
				ID:        walletTxID,
				SessionID: sessionID,
				Status:    ordermodel.StatusPending,
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
		return fmt.Errorf("create confirm fee session and txs: %w", err)
	}

	// Step 5: charge the seller — wallet debit (+ credit compensator), then gateway loop
	if confirmFeeWallet > 0 {
		if _, dErr := r.account.Call().WalletDebit(ctx, accountbiz.WalletDebitParams{
			AccountID: sellerID,
			Amount:    confirmFeeWallet,
			Reference: fmt.Sprintf("tx:%s", walletTxID),
			Note:      "confirm fee wallet payment",
		}); dErr != nil {
			return fmt.Errorf("seller wallet debit: %w", dErr)
		}
		// Arm AFTER debit confirms — arming earlier would over-credit on saga fire if debit never committed.
		r.sg.Defer("credit_wallet", func(ctx restate.Context) error {
			return r.account.Call().WalletCredit(ctx, accountbiz.WalletCreditParams{
				AccountID: sellerID,
				Amount:    confirmFeeWallet,
				Type:      "Refund",
				Reference: fmt.Sprintf("tx:%s", walletTxID),
				Note:      "saga compensate: confirm fee wallet debit",
			})
		})
		if err = restate.RunVoid(ctx, func(rctx restate.RunContext) error {
			_, e := r.Storage.Querier().MarkTransactionSuccess(rctx, orderrepo.MarkTransactionSuccessParams{
				ID:          walletTxID,
				DateSettled: time.Now(),
			})
			return e
		}); err != nil {
			return fmt.Errorf("mark confirm-fee wallet tx success: %w", err)
		}
	}

	if confirmFeeGateway > 0 {
		if err = r.gw.RunPaymentLoop(ctx, gateway.LoopParams{
			SessionID:       sessionID,
			SessionDeadline: time.Now().Add(gateway.SessionExpiry),
			NotePrefix:      "confirm fee gateway payment",
			Description:     fmt.Sprintf("Confirm fee session %s", sessionID),
			PaymentOption:   r.input.PaymentOption,
			Amount:          confirmFeeGateway,
			Currency:        sellerCurrency,
			ErrCancelled:    ordermodel.ErrConfirmCancelled,
			ErrExpired:      ordermodel.ErrConfirmExpired,
		}); err != nil {
			return fmt.Errorf("gateway payment loop: %w", err)
		}
	}

	// Step 6: book the shipment, then create order records — transport + order +
	// item links in one journaled tx.
	// Paid — clear saga; failure here is a programmer bug (seller already paid) → terminal.
	r.sg.Clear()

	// Book the shipment with the provider (journaled — the tracking id must be
	// stable across replay). Its Data carries tracking_id, which the delivery
	// webhook matches on; we merge the quote cost in for the record.
	shipment, err := restate.Run(ctx, func(rctx restate.RunContext) (transport.Transport, error) {
		return transportClient.Create(rctx, transport.CreateParams{
			Items:       transportItems,
			FromAddress: contactMap[sellerID].Address,
			ToAddress:   address,
			Option:      transportOption,
		})
	})
	if err != nil {
		return fmt.Errorf("book transport: %w", err)
	}
	transportData := map[string]any{"quote": quote.Cost}
	_ = json.Unmarshal(shipment.Data, &transportData)

	if err = restate.RunVoid(ctx, func(rctx restate.RunContext) error {
		trData, _ := json.Marshal(transportData)
		transportID, tErr := r.Storage.Querier().CreateTransport(rctx, orderrepo.CreateTransportParams{
			Option: transportOption,
			Data:   json.RawMessage(trData),
		})
		if tErr != nil {
			return fmt.Errorf("db create transport: %w", tErr)
		}
		if oErr := r.Storage.Querier().CreateOrderIdempotent(rctx, orderrepo.CreateOrderIdempotentParams{
			ID:               r.orderID,
			BuyerID:          buyerID,
			SellerID:         sellerID,
			TransportID:      transportID,
			Address:          address,
			DateCreated:      time.Now(),
			ConfirmedByID:    r.input.Account.ID,
			ConfirmSessionID: sessionID,
			Note:             null.NewString(r.input.Note, r.input.Note != ""),
		}); oErr != nil {
			return fmt.Errorf("db create order: %w", oErr)
		}
		if lErr := r.Storage.Querier().SetItemsOrderID(rctx, orderrepo.SetItemsOrderIDParams{
			OrderID: uuid.NullUUID{UUID: r.orderID, Valid: true},
			ItemIds: r.input.ItemIDs,
		}); lErr != nil {
			return fmt.Errorf("db set items order id: %w", lErr)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("create order records: %w", err)
	}

	metrics.OrdersCreatedTotal.Inc()

	// Wallet-only: mark the confirm-fee session paid and unblock the sync caller
	// (else GetPaymentURL hangs on payment_url_1, which is never minted).
	if confirmFeeGateway == 0 {
		if err = r.gw.SettleWalletOnly(ctx, sessionID); err != nil {
			return fmt.Errorf("settle wallet-only confirm fee: %w", err)
		}
	}

	// Step 7: notify the buyer their items are confirmed
	itemNames := lo.Map(orderItems, func(it ordermodel.OrderItem, _ int) string { return it.SkuName })
	if err = r.NotifyOrder(ctx, buyerID, r.orderID, accountmodel.NotiItemsConfirmed, "Items confirmed",
		fmt.Sprintf("%s has been confirmed by the seller.", ordermodel.SummarizeNames(itemNames))); err != nil {
		return fmt.Errorf("notify items confirmed: %w", err)
	}

	r.conf = confirmResult{SellerID: sellerID, PaidTotal: paidTotal, Currency: sellerCurrency}
	return nil
}

// fetchItems wraps the DB list in restate.Run so ordering and missing-row checks are journaled.
func (h *FulfillmentWorkflow) fetchItems(ctx restate.WorkflowContext, itemIDs []int64) ([]ordermodel.OrderItem, error) {
	items, err := restate.Run(ctx, func(rctx restate.RunContext) ([]ordermodel.OrderItem, error) {
		res, e := h.Storage.Querier().ListItem(rctx, orderrepo.ListItemParams{Id: itemIDs})
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

// aggregateItems validates cross-item invariants (ownership, buyer, address, transport) and sums paidTotal.
// Failures are restate.TerminalError — deterministic violations must not retry forever.
func aggregateItems(items []ordermodel.OrderItem, sellerID uuid.UUID) (
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

// splitConfirmFee splits the confirm fee between wallet (up to balance) and gateway (remainder).
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
