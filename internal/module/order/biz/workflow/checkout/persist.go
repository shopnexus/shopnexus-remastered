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

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
)

// persist creates the payment session, optional wallet tx, and order items, then settles the wallet leg.
// Sets r.internalWalletAmount so pay() can derive the gateway amount.
func (r *checkoutRun) persist() error {
	ctx, input := r.ctx, r.input

	// Step 1: split total into wallet leg (up to balance) and gateway remainder
	if input.UseWallet && r.total > 0 {
		balance, err := r.account.GetWalletBalance(ctx, input.Account.ID)
		if err != nil {
			return fmt.Errorf("get wallet balance: %w", err)
		}
		r.internalWalletAmount = min(balance, r.total)
	}
	if r.total-r.internalWalletAmount > 0 && input.PaymentOption == "" {
		return ordermodel.ErrInsufficientWalletBalance
	}

	walletTxID := restate.UUID(ctx)

	// Step 2: atomically create session + wallet tx + order items in one journaled tx
	// session_id marking catches all gateway txs idempotently across retry attempts.
	r.saga.Defer("mark_session_and_txs_failed", func(ctx restate.Context) error {
		return restate.RunVoid(ctx, func(rctx restate.RunContext) error {
			return gateway.MarkSessionFailed(rctx, r.Storage.Querier(), r.sessionID, "checkout saga compensation")
		})
	})

	if err := restate.RunVoid(ctx, func(rctx restate.RunContext) error {
		if _, sErr := r.Storage.Querier().CreateDefaultPaymentSession(rctx, orderdb.CreateDefaultPaymentSessionParams{
			ID:          r.sessionID,
			Kind:        ordermodel.SessionKindBuyerCheckout,
			Status:      orderdb.OrderStatusPending,
			FromID:      uuid.NullUUID{UUID: input.Account.ID, Valid: true},
			Note:        "buyer checkout",
			Currency:    r.buyerCurrency,
			TotalAmount: r.total,
			FxSnapshot:  r.fxSnapshotJSON,
			Data:        json.RawMessage("{}"),
			DateExpired: time.Now().Add(gateway.SessionExpiry),
		}); sErr != nil {
			return fmt.Errorf("db create payment session: %w", sErr)
		}

		if r.internalWalletAmount > 0 {
			if _, txErr := r.Storage.Querier().CreateDefaultTransaction(rctx, orderdb.CreateDefaultTransactionParams{
				ID:        walletTxID,
				SessionID: r.sessionID,
				Status:    orderdb.OrderStatusPending,
				Note:      "checkout wallet payment",
				Data:      json.RawMessage("{}"),
				Amount:    r.internalWalletAmount,
				Currency:  r.buyerCurrency,
			}); txErr != nil {
				return fmt.Errorf("db create wallet tx: %w", txErr)
			}
		}

		for _, item := range input.Items {
			sku := r.skuMap[item.SkuID]
			spu := r.spuMap[sku.SpuID]
			amounts := r.itemAmounts[item.SkuID]

			jsonSerialIDs, mErr := json.Marshal(r.serialIDs[item.SkuID])
			if mErr != nil {
				return fmt.Errorf("marshal serial ids: %w", mErr)
			}

			skuName := spu.Name
			if len(sku.Attributes) > 0 {
				vals := make([]string, 0, len(sku.Attributes))
				for _, attr := range sku.Attributes {
					vals = append(vals, attr.Value)
				}
				skuName += " - " + strings.Join(vals, " / ")
			}

			if _, iErr := r.Storage.Querier().CreateDefaultItem(rctx, orderdb.CreateDefaultItemParams{
				AccountID:        input.Account.ID,
				SellerID:         spu.AccountID,
				SkuID:            sku.ID,
				SpuID:            sku.SpuID,
				SkuName:          skuName,
				Address:          input.Address,
				Note:             null.NewString(item.Note, item.Note != ""),
				SerialIds:        jsonSerialIDs,
				Quantity:         item.Quantity,
				TransportOption:  r.transportQuotes[item.SkuID].Option,
				SubtotalAmount:   amounts.subtotalAmount,
				TotalAmount:      amounts.totalAmount,
				SourceCurrency:   spu.Currency,
				PaymentSessionID: r.sessionID,
			}); iErr != nil {
				return fmt.Errorf("db create item: %w", iErr)
			}
		}
		return nil
	}); err != nil {
		metrics.CheckoutItemsCreatedTotal.WithLabelValues("failure").Inc()
		return fmt.Errorf("create checkout records: %w", err)
	}

	// Step 3: settle the wallet leg — debit, arm credit compensator, mark tx success
	if r.internalWalletAmount > 0 {
		if _, err := r.account.Call().WalletDebit(ctx, accountbiz.WalletDebitParams{
			AccountID: input.Account.ID,
			Amount:    r.internalWalletAmount,
			Reference: fmt.Sprintf("tx:%s", walletTxID),
			Note:      "checkout internal wallet",
		}); err != nil {
			return fmt.Errorf("debit internal wallet: %w", err)
		}
		// Arm AFTER debit confirms — arming earlier would over-credit on saga fire if debit never committed.
		// TODO: xem lại step này, vì đang ko đc hỗ trợ idempotency => có thể double credit/debit
		r.saga.Defer("credit_internal_wallet", func(ctx restate.Context) error {
			return r.account.Call().WalletCredit(ctx, accountbiz.WalletCreditParams{
				AccountID: input.Account.ID,
				Amount:    r.internalWalletAmount,
				Type:      "Refund",
				Reference: fmt.Sprintf("tx:%s", walletTxID),
				Note:      "saga compensate: checkout wallet debit",
			})
		})
		if err := restate.RunVoid(ctx, func(rctx restate.RunContext) error {
			_, e := r.Storage.Querier().MarkTransactionSuccess(rctx, orderdb.MarkTransactionSuccessParams{
				ID:          walletTxID,
				DateSettled: time.Now(),
			})
			return e
		}); err != nil {
			return fmt.Errorf("mark wallet tx success: %w", err)
		}
	}

	return nil
}
