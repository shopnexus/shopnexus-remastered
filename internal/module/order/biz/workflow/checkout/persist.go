package checkout

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	restate "github.com/restatedev/sdk-go"

	"shopnexus-server/internal/infras/metrics"
	accountbiz "shopnexus-server/internal/module/account/biz"
	"shopnexus-server/internal/module/order/biz/workflow/gateway"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/shared/saga"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
)

// persist resolves the wallet/gateway split, atomically creates the
// payment_session, the (optional) Pending wallet tx and the order items, then
// settles the internal wallet leg. It returns the amount charged to the wallet
// so the caller can derive the gateway amount. Gateway txs are NOT created here
// — they're minted per-attempt inside the payment loop.
func (h *CheckoutWorkflow) persist(
	ctx restate.WorkflowContext,
	saga *saga.Saga,
	input CheckoutWorkflowInput,
	priced pricing,
	serialIDsMap map[uuid.UUID][]string,
	sessionID uuid.UUID,
) (int64, error) {
	var internalWalletAmount int64
	if input.UseWallet && priced.total > 0 {
		balance, err := h.account.GetWalletBalance(ctx, input.Account.ID)
		if err != nil {
			return 0, fmt.Errorf("get wallet balance: %w", err)
		}
		internalWalletAmount = min(balance, priced.total)
	}
	if priced.total-internalWalletAmount > 0 && input.PaymentOption == "" {
		return 0, ordermodel.ErrInsufficientWalletBalance
	}

	internalWalletTxID := restate.UUID(ctx)

	// Compensator: mark the session + every still-Pending child tx Failed by
	// session_id. Multi-attempt sessions spawn N gateway txs across the loop,
	// so the compensator can't track IDs explicitly — session-wide marking
	// catches them all idempotently.
	saga.Defer("mark_session_and_txs_failed", func(ctx restate.Context) error {
		return restate.RunVoid(ctx, func(rctx restate.RunContext) error {
			return gateway.MarkSessionFailed(rctx, h.Storage.Querier(), sessionID, "checkout saga compensation")
		})
	})

	// Result fields aren't needed downstream, but journaling them gives a clean
	// replay trace for debugging.
	type checkoutRunResult struct {
		Session orderdb.OrderPaymentSession `json:"session"`
		Items   []orderdb.OrderItem         `json:"items"`
	}
	if _, err := restate.Run(ctx, func(rctx restate.RunContext) (checkoutRunResult, error) {
		var res checkoutRunResult

		session, sErr := h.Storage.Querier().
			CreateDefaultPaymentSession(rctx, orderdb.CreateDefaultPaymentSessionParams{
				ID:          sessionID,
				Kind:        ordermodel.SessionKindBuyerCheckout,
				Status:      orderdb.OrderStatusPending,
				FromID:      uuid.NullUUID{UUID: input.Account.ID, Valid: true},
				ToID:        uuid.NullUUID{},
				Note:        "buyer checkout",
				Currency:    priced.buyerCurrency,
				TotalAmount: priced.total,
				FxSnapshot:  priced.fxSnapshotJSON,
				Data:        json.RawMessage("{}"),
				DatePaid:    null.Time{},
				DateExpired: time.Now().Add(gateway.SessionExpiry),
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
				Currency:      priced.buyerCurrency,
				ReversesID:    uuid.NullUUID{},
				DateSettled:   null.Time{},
				DateExpired:   null.Time{},
			}); txErr != nil {
				return res, fmt.Errorf("db create wallet tx: %w", txErr)
			}
		}

		for _, checkoutItem := range input.Items {
			sku := priced.skuMap[checkoutItem.SkuID]
			spu := priced.spuMap[sku.SpuID]
			amounts := priced.itemAmounts[checkoutItem.SkuID]
			tq := priced.transportQuotes[checkoutItem.SkuID]

			jsonSerialIDs, mErr := json.Marshal(serialIDsMap[checkoutItem.SkuID])
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
	}); err != nil {
		metrics.CheckoutItemsCreatedTotal.WithLabelValues("failure").Inc()
		return 0, fmt.Errorf("create checkout records: %w", err)
	}

	// Internal wallet payment. The wallet tx was created Pending above; mark it
	// Success after the debit acknowledges.
	if internalWalletAmount > 0 {
		if _, err := h.account.Call().WalletDebit(ctx, accountbiz.WalletDebitParams{
			AccountID: input.Account.ID,
			Amount:    internalWalletAmount,
			Reference: fmt.Sprintf("tx:%s", internalWalletTxID),
			Note:      "checkout internal wallet",
		}); err != nil {
			return 0, fmt.Errorf("debit internal wallet: %w", err)
		}
		// Arm the credit compensator AFTER the debit confirms. WalletDebit is
		// atomic (single CTE under FOR UPDATE) → terminal failure means no debit
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
		if err := restate.RunVoid(ctx, func(rctx restate.RunContext) error {
			_, e := h.Storage.Querier().MarkTransactionSuccess(rctx, orderdb.MarkTransactionSuccessParams{
				ID:          internalWalletTxID,
				DateSettled: time.Now(),
			})
			return e
		}); err != nil {
			return 0, fmt.Errorf("mark wallet tx success: %w", err)
		}
	}

	return internalWalletAmount, nil
}
