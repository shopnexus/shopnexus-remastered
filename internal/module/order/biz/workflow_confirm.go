package orderbiz

import (
	"encoding/json"
	"fmt"
	"time"

	restate "github.com/restatedev/sdk-go"

	"shopnexus-server/internal/infras/metrics"
	accountbiz "shopnexus-server/internal/module/account/biz"
	accountmodel "shopnexus-server/internal/module/account/model"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/provider/payment"
	"shopnexus-server/internal/provider/transport"
	"shopnexus-server/internal/shared/saga"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/samber/lo"
)

type ConfirmWorkflow struct {
	base *OrderHandler
}

func NewConfirmWorkflow(base *OrderHandler) *ConfirmWorkflow {
	return &ConfirmWorkflow{base: base}
}
func (h *ConfirmWorkflow) ServiceName() string { return "ConfirmWorkflow" }

func (h *ConfirmWorkflow) Run(
	ctx restate.WorkflowContext,
	input ConfirmWorkflowInput,
) (out ConfirmWorkflowOutput, err error) {
	defer metrics.TrackHandler("confirm_workflow", "Run", &err)()

	// Workflow ID set at submission time IS the confirm-fee session UUID —
	// every downstream insert / log / webhook routing key uses it.
	workflowID := uuid.MustParse(restate.Key(ctx))
	sessionID := workflowID

	// Step 1: Validate.
	if err = validator.Validate(input); err != nil {
		return out, fmt.Errorf("validate confirm: %w", err)
	}

	saga := saga.New(ctx)
	// Reject WaitPaymentURL on terminal failure so the synchronous HTTP
	// caller doesn't hang when Run dies before the gateway loop ever
	// resolves payment_url_1. No-op if already resolved.
	saga.Defer("reject_payment_url", func(_ restate.Context) error {
		return restate.Promise[string](ctx, "payment_url_1").Reject(err)
	})
	defer func() {
		if restate.IsTerminalError(err) {
			saga.Compensate()
		}
	}()

	sellerID := input.Account.ID

	// Step 2: Lock seller pending so two concurrent confirms over an
	// overlapping ItemIDs slice can't double-spend wallet balance or skip
	// validation. The lock's lifetime is the workflow run.
	unlock := h.base.locker.Lock(ctx, fmt.Sprintf("order:seller-pending:%s", sellerID))
	defer unlock()

	// Step 3: Fetch and validate items inside a Run so list ordering and
	// missing-row checks are journaled (replay returns the exact same slice).
	orderItems, err := restate.Run(ctx, func(rctx restate.RunContext) ([]orderdb.OrderItem, error) {
		items, e := h.base.storage.Querier().ListItem(rctx, orderdb.ListItemParams{
			ID: input.ItemIDs,
		})
		if e != nil {
			return nil, fmt.Errorf("db list items: %w", e)
		}
		if len(items) != len(input.ItemIDs) {
			return nil, ordermodel.ErrOrderItemNotFound
		}
		return items, nil
	})
	if err != nil {
		return out, fmt.Errorf("fetch items: %w", err)
	}

	// Validate items and aggregate shared fields. Every item must be owned
	// by this seller, share buyer/address/transport, and not already be
	// final. paidTotal is the sum the buyer already paid for these items —
	// the eventual payout amount when escrow releases.
	var (
		buyerID                 uuid.UUID
		address                 string
		transportOption         string
		paidTotal               int64
		uniquePaymentSessionIDs = make(map[uuid.UUID]struct{})
	)
	// Validation outcome is deterministic over journaled orderItems, so any
	// failure must be terminal — otherwise Restate would retry the same
	// invariant violation forever. fmt.Errorf("%w") strips the
	// marker on ordermodel errors, so we re-wrap with restate.TerminalError.
	for i, item := range orderItems {
		if item.OrderID.Valid {
			return out, restate.TerminalError(fmt.Errorf("item %d: %w", item.ID, ordermodel.ErrItemAlreadyConfirmed))
		}
		if item.DateCancelled.Valid {
			return out, restate.TerminalError(fmt.Errorf("item %d: %w", item.ID, ordermodel.ErrItemAlreadyCancelled))
		}
		if item.SellerID != sellerID {
			return out, restate.TerminalError(fmt.Errorf("item %d: %w", item.ID, ordermodel.ErrItemNotOwnedBySeller))
		}
		if i == 0 {
			buyerID = item.AccountID
			address = item.Address
			transportOption = item.TransportOption
		} else {
			if item.AccountID != buyerID {
				return out, restate.TerminalError(fmt.Errorf("item %d: %w", item.ID, ordermodel.ErrItemsNotSameBuyer))
			}
			if item.Address != address {
				return out, restate.TerminalError(fmt.Errorf("item %d: %w", item.ID, ordermodel.ErrItemsNotSameAddress))
			}
			if item.TransportOption != transportOption {
				return out, restate.TerminalError(fmt.Errorf("item %d: %w", item.ID, ordermodel.ErrItemsTransportMismatch))
			}
		}
		paidTotal += item.TotalAmount
		uniquePaymentSessionIDs[item.PaymentSessionID] = struct{}{}
	}

	// Step 4: Verify every unique buyer payment session already settled.
	// Confirming items the buyer never paid for would mint inventory for
	// free.
	for psID := range uniquePaymentSessionIDs {
		status, sErr := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderStatus, error) {
			session, e := h.base.storage.Querier().GetPaymentSession(rctx, uuid.NullUUID{UUID: psID, Valid: true})
			if e != nil {
				return "", fmt.Errorf("get payment session: %w", e)
			}
			return session.Status, nil
		})
		if sErr != nil {
			return out, fmt.Errorf("check payment session status: %w", sErr)
		}
		if status != orderdb.OrderStatusSuccess {
			return out, ordermodel.ErrPaymentNotSuccess
		}
	}

	// Step 5: Quote transport for the aggregate shipment. One quote covers
	// all confirmed items because they share the same transport_option and
	// destination address (asserted above).
	contactMap, err := h.base.account.GetDefaultContact(ctx, []uuid.UUID{sellerID})
	if err != nil {
		return out, fmt.Errorf("get seller contact: %w", err)
	}
	fromAddress := contactMap[sellerID].Address

	transportClient, err := h.base.getTransportClient(transportOption)
	if err != nil {
		return out, err
	}

	transportItems := lo.Map(orderItems, func(item orderdb.OrderItem, _ int) transport.ItemMetadata {
		return transport.ItemMetadata{SkuID: item.SkuID, Quantity: item.Quantity}
	})
	quote, err := transportClient.Quote(ctx, transport.QuoteParams{
		Items:       transportItems,
		FromAddress: fromAddress,
		ToAddress:   address,
	})
	if err != nil {
		return out, fmt.Errorf("quote transport: %w", err)
	}

	platformFee := int64(0) // TODO: plug config
	confirmFeeTotal := quote.Cost + platformFee

	// Confirm-fee txs are denominated in the seller's currency (the seller
	// is paying the platform). inferCurrency is cross-module → outside Run.
	sellerCurrency, err := h.base.InferCurrency(ctx, sellerID)
	if err != nil {
		return out, fmt.Errorf("infer seller currency: %w", err)
	}

	// Step 6: Wallet / gateway split for confirmFeeTotal.
	var confirmFeeWallet, confirmFeeGateway int64
	if input.UseWallet && confirmFeeTotal > 0 {
		balance, balErr := h.base.account.GetWalletBalance(ctx, sellerID)
		if balErr != nil {
			return out, fmt.Errorf("get seller wallet balance: %w", balErr)
		}
		if balance >= confirmFeeTotal {
			confirmFeeWallet = confirmFeeTotal
		} else {
			confirmFeeWallet = balance
		}
	}
	confirmFeeGateway = confirmFeeTotal - confirmFeeWallet

	if confirmFeeGateway > 0 && input.PaymentOption == "" {
		return out, ordermodel.ErrInsufficientWalletBalance
	}

	// Step 7: Atomically create payment_session and child txs (Pending) in
	// one Run. Order / transport / item-link rows are deferred to the
	// post-paid Run below, so we don't have to compensate them on rollback.
	// Tx UUIDs are pre-allocated outside the closure: restate.UUID journals
	// them, so retries reuse the same value and INSERTs are idempotent on
	// PK conflict.
	// Wallet tx ID is pre-allocated (single-shot). Gateway txs are minted
	// per attempt inside the retry loop below, so the compensator marks every
	// still-Pending child tx by session_id rather than tracking IDs.
	walletTxID := restate.UUID(ctx)

	saga.Defer("mark_session_and_txs_failed", func(ctx restate.Context) error {
		return restate.RunVoid(ctx, func(rctx restate.RunContext) error {
			return markSessionAndAllPendingFailed(rctx, h.base.storage.Querier(), sessionID, "confirm saga compensation")
		})
	})

	if err = restate.RunVoid(ctx, func(rctx restate.RunContext) error {
		if _, sErr := h.base.storage.Querier().CreateDefaultPaymentSession(rctx, orderdb.CreateDefaultPaymentSessionParams{
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
			DateExpired: time.Now().Add(sessionExpiry),
		}); sErr != nil {
			return fmt.Errorf("db create confirm session: %w", sErr)
		}

		if confirmFeeWallet > 0 {
			if _, txErr := h.base.storage.Querier().CreateDefaultTransaction(rctx, orderdb.CreateDefaultTransactionParams{
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

		// Gateway txs are minted per-attempt below.
		return nil
	}); err != nil {
		return out, fmt.Errorf("create confirm fee session and txs: %w", err)
	}

	// Step 8: Wallet debit + mark wallet tx success.
	if confirmFeeWallet > 0 {
		if _, dErr := h.base.account.WalletDebit(ctx, accountbiz.WalletDebitParams{
			AccountID: sellerID,
			Amount:    confirmFeeWallet,
			Reference: fmt.Sprintf("tx:%s", walletTxID),
			Note:      "confirm fee wallet payment",
		}); dErr != nil {
			return out, fmt.Errorf("seller wallet debit: %w", dErr)
		}
		// Arm credit compensator AFTER debit confirmed. WalletDebit is atomic
		// (single CTE under FOR UPDATE) → terminal failure means no debit,
		// so arming earlier would over-credit on saga fire.
		saga.Defer("credit_wallet", func(ctx restate.Context) error {
			return h.base.account.WalletCredit(ctx, accountbiz.WalletCreditParams{
				AccountID: sellerID,
				Amount:    confirmFeeWallet,
				Type:      "Refund",
				Reference: fmt.Sprintf("tx:%s", walletTxID),
				Note:      "saga compensate: confirm fee wallet debit",
			})
		})
		if mErr := restate.RunVoid(ctx, func(rctx restate.RunContext) error {
			_, e := h.base.storage.Querier().MarkTransactionSuccess(rctx, orderdb.MarkTransactionSuccessParams{
				ID:          walletTxID,
				DateSettled: time.Now(),
			})
			return e
		}); mErr != nil {
			return out, fmt.Errorf("mark confirm-fee wallet tx success: %w", mErr)
		}
	}

	// Step 9–10: Gateway payment loop (skipped for wallet-only). Shared with
	// CheckoutWorkflow via runGatewayPaymentLoop in payment_gateway.go.
	if confirmFeeGateway > 0 {
		if err = h.base.runGatewayPaymentLoop(ctx, gatewayPaymentLoopParams{
			SessionID:       sessionID,
			WorkflowID:      workflowID,
			SessionDeadline: time.Now().Add(sessionExpiry),
			NotePrefix:      "confirm fee gateway payment",
			Description:     fmt.Sprintf("Confirm fee session %s", workflowID),
			PaymentOption:   input.PaymentOption,
			Amount:          confirmFeeGateway,
			Currency:        sellerCurrency,
			ErrCancelled:    ordermodel.ErrConfirmCancelled,
			ErrExpired:      ordermodel.ErrConfirmExpired,
		}); err != nil {
			return out, err
		}
	}

	// Step 11: Paid path — clear saga and atomically create transport,
	// order, and item links in one Run. Failure here is a programmer bug
	// (the seller already paid us); we let it bubble as terminal.
	saga.Clear()

	type postPayRunResult struct {
		Order     orderdb.OrderOrder     `json:"order"`
		Transport orderdb.OrderTransport `json:"transport"`
	}
	postPay, err := restate.Run(ctx, func(rctx restate.RunContext) (postPayRunResult, error) {
		var res postPayRunResult

		quoteData, _ := json.Marshal(map[string]int64{"quote": quote.Cost})
		trRow, tErr := h.base.storage.Querier().CreateDefaultTransport(rctx, orderdb.CreateDefaultTransportParams{
			Option: transportOption,
			Data:   json.RawMessage(quoteData),
		})
		if tErr != nil {
			return res, fmt.Errorf("db create transport: %w", tErr)
		}

		// TODO: remove this mock when a real transport provider is wired up.
		// Mark the seller→buyer forward leg Success immediately so the order
		// flips to Completed in the FE without waiting for a real webhook.
		mockData, _ := json.Marshal(map[string]any{
			"quote": quote.Cost,
			"mock":  "auto-delivered",
		})
		trRow, tErr = h.base.storage.Querier().UpdateTransportStatusByID(rctx, orderdb.UpdateTransportStatusByIDParams{
			ID:     trRow.ID,
			Status: orderdb.NullOrderStatus{OrderStatus: orderdb.OrderStatusSuccess, Valid: true},
			Data:   json.RawMessage(mockData),
		})
		if tErr != nil {
			return res, fmt.Errorf("mock transport success: %w", tErr)
		}
		res.Transport = trRow

		order, oErr := h.base.storage.Querier().CreateDefaultOrder(rctx, orderdb.CreateDefaultOrderParams{
			BuyerID:          buyerID,
			SellerID:         sellerID,
			TransportID:      trRow.ID,
			Address:          address,
			ConfirmedByID:    input.Account.ID,
			ConfirmSessionID: sessionID,
			Note:             null.NewString(input.Note, input.Note != ""),
		})
		if oErr != nil {
			return res, fmt.Errorf("db create order: %w", oErr)
		}
		res.Order = order

		if lErr := h.base.storage.Querier().SetItemsOrderID(rctx, orderdb.SetItemsOrderIDParams{
			OrderID: uuid.NullUUID{UUID: order.ID, Valid: true},
			ItemIds: input.ItemIDs,
		}); lErr != nil {
			return res, fmt.Errorf("db set items order id: %w", lErr)
		}

		return res, nil
	})
	if err != nil {
		return out, err
	}

	metrics.OrdersCreatedTotal.Inc()

	// Step 12: Notify buyer (fire-and-forget).
	itemNames := lo.Map(orderItems, func(it orderdb.OrderItem, _ int) string { return it.SkuName })
	summary := ordermodel.SummarizeNames(itemNames)
	notiMeta, _ := json.Marshal(map[string]string{"order_id": postPay.Order.ID.String()})
	restate.ServiceSend(ctx, "Account", "CreateNotification").Send(accountbiz.CreateNotificationParams{
		AccountID: buyerID,
		Type:      accountmodel.NotiItemsConfirmed,
		Channel:   accountmodel.ChannelInApp,
		Title:     "Items confirmed",
		Content:   fmt.Sprintf("%s has been confirmed by the seller.", summary),
		Metadata:  notiMeta,
	})

	// Step 13: Hand off to PayoutWorkflow. Its workflow ID is the order ID,
	// so the eventual escrow release / refund signals can target it without
	// a side table.
	restate.WorkflowSend(ctx, "PayoutWorkflow", postPay.Order.ID.String(), "Run").Send(PayoutInput{
		OrderID:   postPay.Order.ID,
		SellerID:  sellerID,
		PaidTotal: paidTotal,
		Currency:  sellerCurrency,
	})

	return ConfirmWorkflowOutput{
		Status:           "confirmed",
		OrderID:          postPay.Order.ID,
		ConfirmSessionID: sessionID,
	}, nil
}

// WaitPaymentURL blocks until Run resolves the FIRST attempt's URL. Used by
// the sync /seller/pending/confirm HTTP handler. Subsequent retries go
// through RequestNewPaymentURL.
func (h *ConfirmWorkflow) WaitPaymentURL(
	ctx restate.WorkflowSharedContext,
	_ struct{},
) (string, error) {
	return restate.Promise[string](ctx, "payment_url_1").Result()
}

// RequestNewPaymentURL is the multi-attempt entry point. Caller has already
// verified the latest gateway tx is Failed/expired. We resolve the current
// attempt's retry promise (idempotent) so Run advances to attempt+1, then
// block on the new URL.
func (h *ConfirmWorkflow) RequestNewPaymentURL(
	ctx restate.WorkflowSharedContext,
	_ struct{},
) (string, error) {
	attempt, err := restate.Get[int](ctx, "payment_attempt")
	if err != nil {
		return "", fmt.Errorf("read payment_attempt state: %w", err)
	}
	if attempt < 1 {
		return "", ordermodel.ErrConfirmExpired
	}
	_ = restate.Promise[struct{}](ctx, fmt.Sprintf("retry_%d", attempt)).Resolve(struct{}{})
	return restate.Promise[string](ctx, fmt.Sprintf("payment_url_%d", attempt+1)).Result()
}

// PaymentNotification is called by the payment provider via OrderHandler.
// OnPaymentResult. The webhook's RefID is the gateway tx UUID — we key the
// promise by it so late webhooks for already-Failed prior attempts are
// silently no-ops.
func (h *ConfirmWorkflow) PaymentNotification(
	ctx restate.WorkflowSharedContext,
	noti payment.Notification,
) error {
	return restate.Promise[payment.Notification](ctx, "payment_event_"+noti.RefID).Resolve(noti)
}

// CancelConfirm lets the seller abort an in-flight confirm.
func (h *ConfirmWorkflow) CancelConfirm(
	ctx restate.WorkflowSharedContext,
	_ struct{},
) error {
	return restate.Promise[struct{}](ctx, "user_cancel").Resolve(struct{}{})
}

type ConfirmWorkflowInput struct {
	Account       accountmodel.AuthenticatedAccount `json:"account"`
	ItemIDs       []int64                           `json:"item_ids" validate:"required,min=1,max=1000"`
	UseWallet     bool                              `json:"use_wallet"`
	WalletID      *uuid.UUID                        `json:"wallet_id,omitempty"`
	PaymentOption string                            `json:"payment_option" validate:"max=100"`
	Note          string                            `json:"note" validate:"max=500"`
}

type ConfirmWorkflowOutput struct {
	Status           string    `json:"status"`
	OrderID          uuid.UUID `json:"order_id,omitempty"`
	ConfirmSessionID uuid.UUID `json:"confirm_session_id"`
}
