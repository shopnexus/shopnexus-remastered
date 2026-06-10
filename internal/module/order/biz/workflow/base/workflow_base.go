// Package base holds the workflow-shared core: the gateway payment loop and
// the refund credit flow, used by both workflows and the refund-deciding
// handlers.
package base

import (
	"encoding/json"
	"fmt"
	"time"

	accountbiz "shopnexus-server/internal/module/account/biz"
	inventorybiz "shopnexus-server/internal/module/inventory/biz"
	inventorydb "shopnexus-server/internal/module/inventory/db/sqlc"
	orderbase "shopnexus-server/internal/module/order/biz/base"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/provider/payment"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	restate "github.com/restatedev/sdk-go"
	"github.com/samber/lo"
)

// Context and WorkflowContext mirror their restate counterparts under
// package-local names. restate.Reflect matches context parameter types
// exactly, so methods declared with these are skipped when *Base is embedded
// in a workflow / handler struct — embedding stays safe while the methods
// remain callable with any real restate context.
type (
	Context         interface{ restate.Context }
	WorkflowContext interface{ restate.WorkflowContext }
)

const (
	// paymentExpiry bounds a single gateway payment attempt — i.e. how long the
	// user has to complete one VNPay/Momo redirect before that URL is considered
	// dead. Multi-attempt sessions can spawn another attempt (up to SessionExpiry).
	paymentExpiry = 30 * time.Minute
	// SessionExpiry bounds the entire checkout/confirm session across all retry
	// attempts. Once it elapses, the session is failed via saga regardless of
	// whether the buyer is mid-attempt.
	SessionExpiry = 24 * time.Hour
)

// Base carries the workflow-shared dependency set on top of the module core.
type Base struct {
	*orderbase.Base

	account   accountbiz.AccountBizClient
	inventory inventorybiz.InventoryBizClient
}

func New(
	c *orderbase.Base,
	account accountbiz.AccountBizClient,
	inventory inventorybiz.InventoryBizClient,
) *Base {
	return &Base{c, account, inventory}
}

// initGatewayPayment charges the gateway for one attempt and persists the
// redirect URL on the attempt's tx.
func (b *Base) initGatewayPayment(
	ctx Context,
	txID uuid.UUID,
	amount int64,
	paymentOption, description string,
) (string, error) {
	if amount <= 0 {
		return "", nil
	}

	paymentClient, err := b.GetPaymentClient(paymentOption)
	if err != nil {
		return "", fmt.Errorf("get payment client: %w", err)
	}

	url, err := restate.Run(ctx, func(rctx restate.RunContext) (string, error) {
		r, e := paymentClient.Charge(rctx, payment.ChargeParams{
			RefID:       txID.String(),
			Amount:      amount,
			Description: description,
		})
		if e != nil {
			return "", e
		}
		return r.RedirectURL, nil
	})
	if err != nil {
		return "", fmt.Errorf("create gateway payment: %w", err)
	}

	if url != "" {
		if pErr := restate.RunVoid(ctx, func(rctx restate.RunContext) error {
			data, _ := json.Marshal(map[string]string{"gateway_url": url})
			return b.Storage.Querier().SetTransactionData(rctx, orderdb.SetTransactionDataParams{
				ID:   txID,
				Data: data,
			})
		}); pErr != nil {
			return "", fmt.Errorf("persist gateway url on tx: %w", pErr)
		}
	}

	return url, nil
}

// GatewayPaymentLoopParams configures RunGatewayPaymentLoop for a specific
// workflow (CheckoutWorkflow vs FulfillmentWorkflow). Differences are pure data:
// amounts, currencies, error mapping, log strings.
type GatewayPaymentLoopParams struct {
	SessionID       uuid.UUID
	WorkflowID      uuid.UUID
	SessionDeadline time.Time

	NotePrefix    string // tx.note prefix, e.g. "checkout gateway payment"
	Description   string // gateway transaction memo, e.g. "Checkout session %s"
	PaymentOption string
	Amount        int64
	Currency      string // Rail debit currency

	ErrCancelled error
	ErrExpired   error
}

// RunGatewayPaymentLoop drives the multi-attempt gateway payment leg shared
// by CheckoutWorkflow and FulfillmentWorkflow. Each iteration: mints a fresh
// gateway tx (Pending, expires now+paymentExpiry), calls initGatewayPayment,
// resolves payment_url_<attempt>. Then waits on:
//
//   - paymentPromise (payment_event_<txID>): gateway settled. On Success,
//     marks the tx + session Success and returns nil.
//   - cancelPromise:  buyer/seller signalled cancel → terminal ErrCancelled.
//   - attempt-expiry: this attempt's URL window elapsed → mark tx Failed,
//     wait for retry_<attempt> (resolved by RequestNewPaymentURL shared
//     handler) and loop into the next attempt.
//   - session-expiry: overall session deadline elapsed → terminal ErrExpired.
//
// Caller is responsible for: workflow-level saga registration, the
// pre-loop session/wallet-tx creation, and the post-success tail. This
// helper only owns the gateway leg.
func (b *Base) RunGatewayPaymentLoop(
	ctx WorkflowContext,
	p GatewayPaymentLoopParams,
) error {
	cancelPromise := restate.Promise[struct{}](ctx, "user_cancel")
	var attempt int

paymentLoop:
	for {
		attempt++
		restate.Set[int](ctx, "payment_attempt", attempt)
		attemptTxID := restate.UUID(ctx)

		if cErr := restate.RunVoid(ctx, func(rctx restate.RunContext) error {
			_, e := b.Storage.Querier().CreateDefaultTransaction(rctx, orderdb.CreateDefaultTransactionParams{
				ID:            attemptTxID,
				SessionID:     p.SessionID,
				Status:        orderdb.OrderStatusPending,
				Note:          fmt.Sprintf("%s (attempt %d)", p.NotePrefix, attempt),
				Error:         null.String{},
				PaymentOption: null.StringFrom(p.PaymentOption),
				Data:          json.RawMessage("{}"),
				Amount:        p.Amount,
				Currency:      p.Currency,
				ReversesID:    uuid.NullUUID{},
				DateSettled:   null.Time{},
				DateExpired:   null.TimeFrom(time.Now().Add(paymentExpiry)),
			})
			return e
		}); cErr != nil {
			return fmt.Errorf("db create gateway tx: %w", cErr)
		}

		url, gErr := b.initGatewayPayment(
			ctx,
			attemptTxID,
			p.Amount,
			p.PaymentOption,
			fmt.Sprintf("%s (attempt %d)", p.Description, attempt),
		)
		if gErr != nil {
			return gErr
		}
		if pErr := restate.Promise[string](ctx, fmt.Sprintf("payment_url_%d", attempt)).Resolve(url); pErr != nil {
			return fmt.Errorf("resolve payment url promise: %w", pErr)
		}

		paymentPromise := restate.Promise[payment.Notification](ctx, "payment_event_"+attemptTxID.String())
		attemptExpiryFut := restate.After(ctx, paymentExpiry)
		sessionRem := time.Until(p.SessionDeadline)
		if sessionRem <= 0 {
			return p.ErrExpired
		}
		sessionExpiryFut := restate.After(ctx, sessionRem)

		done, werr := restate.WaitFirst(ctx, paymentPromise, cancelPromise, attemptExpiryFut, sessionExpiryFut)
		if werr != nil {
			return fmt.Errorf("wait payment event: %w", werr)
		}
		switch done {
		case paymentPromise:
			ev, evErr := paymentPromise.Result()
			if evErr != nil {
				return fmt.Errorf("read payment event: %w", evErr)
			}
			switch ev.Status {
			case payment.StatusSuccess:
				// Promote this attempt's gateway tx + the session to Success.
				// Both queries guard on status='Pending' so they're idempotent
				// on workflow replay.
				if mErr := restate.RunVoid(ctx, func(rctx restate.RunContext) error {
					now := time.Now()
					if _, e := b.Storage.Querier().MarkTransactionSuccess(rctx, orderdb.MarkTransactionSuccessParams{
						ID:          attemptTxID,
						DateSettled: now,
					}); e != nil {
						return fmt.Errorf("mark gateway tx success: %w", e)
					}
					if _, e := b.Storage.Querier().MarkPaymentSessionSuccess(rctx, orderdb.MarkPaymentSessionSuccessParams{
						ID:       p.SessionID,
						DatePaid: now,
					}); e != nil {
						return fmt.Errorf("mark payment session success: %w", e)
					}
					return nil
				}); mErr != nil {
					return mErr
				}
				break paymentLoop
			case payment.StatusFailed, payment.StatusExpired:
				return ordermodel.ErrPaymentFailed
			default:
				return fmt.Errorf("unknown payment event status: %w", ordermodel.ErrPaymentFailed)
			}
		case cancelPromise:
			return p.ErrCancelled
		case sessionExpiryFut:
			return p.ErrExpired
		case attemptExpiryFut:
			// This attempt's URL is dead. Mark its tx Failed and wait for the
			// caller to ask for a fresh one (RequestNewPaymentURL resolves
			// retry_<attempt>). Lazy retry: we don't burn gateway quota until
			// the user actually comes back.
			if mErr := restate.RunVoid(ctx, func(rctx restate.RunContext) error {
				return b.Storage.Querier().MarkTransactionsFailed(rctx, orderdb.MarkTransactionsFailedParams{
					ID:    []uuid.UUID{attemptTxID},
					Error: null.StringFrom("gateway attempt expired"),
				})
			}); mErr != nil {
				return fmt.Errorf("mark attempt failed: %w", mErr)
			}

			retryPromise := restate.Promise[struct{}](ctx, fmt.Sprintf("retry_%d", attempt))
			sessionRem2 := time.Until(p.SessionDeadline)
			if sessionRem2 <= 0 {
				return p.ErrExpired
			}
			sessionExpiryFut2 := restate.After(ctx, sessionRem2)

			done2, werr2 := restate.WaitFirst(ctx, retryPromise, cancelPromise, sessionExpiryFut2)
			if werr2 != nil {
				return werr2
			}
			switch done2 {
			case retryPromise:
				continue paymentLoop
			case cancelPromise:
				return p.ErrCancelled
			case sessionExpiryFut2:
				return p.ErrExpired
			}
		}
	}
	return nil
}

// CreditFromSession credits the recipient with the sum of positive-amount Success
// transactions in the given session. Use this when a session is being voided or
// refunded — credits only legs that actually settled, never minting balance for
// unsettled / failed / pending legs. Returns the amount credited; 0 means no-op.
func (b *Base) CreditFromSession(
	ctx Context,
	params ordermodel.CreditFromSessionParams,
) (int64, error) {
	settled, err := restate.Run(ctx, func(ctx restate.RunContext) (int64, error) {
		txs, err := b.Storage.Querier().ListTransactionsBySession(ctx, params.SessionID)
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
		return 0, fmt.Errorf("read session txs: %w", err)
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

// ExecuteRefundCredit performs the actual credit flow: insert refund tx,
// flip refund.status to Accepted, cancel items, credit buyer wallet, restock
// inventory. Used by all 3 paths that end in Accepted (seller approve,
// auto-accept timeout, admin dismiss). Callers outside the fulfillment
// workflow must signal it afterwards (Send().OnRefundChanged) so its escrow
// loop re-snapshots; the in-workflow caller re-snapshots on loop continue.
func (b *Base) ExecuteRefundCredit(
	ctx Context,
	refund orderdb.OrderRefund,
	deciderID uuid.UUID,
	reason ordermodel.RefundCreditReason,
) (orderdb.OrderRefund, error) {
	var zero orderdb.OrderRefund

	items, err := restate.Run(ctx, func(rctx restate.RunContext) ([]orderdb.OrderItem, error) {
		res, e := b.Storage.Querier().ListItem(rctx, orderdb.ListItemParams{
			OrderId: []uuid.UUID{refund.OrderID},
		})
		if e != nil {
			return nil, e
		}
		return res.Data, nil
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
		return b.Storage.Querier().GetOrder(rctx, orderdb.GetOrderParams{
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
		return b.Storage.Querier().ListTransactionsBySession(rctx, anyItem.PaymentSessionID)
	})
	if err != nil {
		return zero, fmt.Errorf("list session txs: %w", err)
	}
	originalTx, ok := ordermodel.FindOriginalCharge(sessionTxs)
	if !ok {
		return zero, fmt.Errorf("no original tx: %w", ordermodel.ErrOrderItemNotFound)
	}
	originalTxID := uuid.NullUUID{UUID: originalTx.ID, Valid: true}

	// deterministic key: retries must reuse it so the idempotency ledger dedupes
	refundTxID := uuid.NewSHA1(uuid.NameSpaceOID, fmt.Appendf(nil, "refund-credit:refund:%s", refund.ID))
	updated, err := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderRefund, error) {
		refundTx, e := b.Storage.Querier().CreateDefaultTransaction(rctx, orderdb.CreateDefaultTransactionParams{
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
			u, e = b.Storage.Querier().SellerApproveRefund(rctx, orderdb.SellerApproveRefundParams{
				ID:         refund.ID,
				RefundTxID: uuid.NullUUID{UUID: refundTx.ID, Valid: true},
			})
		case orderdb.OrderRefundStatusDisputed:
			u, e = b.Storage.Querier().AdminDismissDispute(rctx, orderdb.AdminDismissDisputeParams{
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
			if _, ce := b.Storage.Querier().CancelItem(rctx, orderdb.CancelItemParams{
				ID:            it.ID,
				CancelledByID: uuid.NullUUID{UUID: deciderID, Valid: true},
			}); ce != nil {
				return orderdb.OrderRefund{}, fmt.Errorf("cancel item: %w", ce)
			}
		}
		return u, nil
	})
	if err != nil {
		return zero, fmt.Errorf("execute refund ops: %w", err)
	}

	if _, err := b.CreditFromSession(ctx, ordermodel.CreditFromSessionParams{
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

	return updated, nil
}
