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
	"shopnexus-server/internal/shared/saga"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
)

type PayoutWorkflow struct {
	base *OrderHandler
}

func NewPayoutWorkflow(base *OrderHandler) *PayoutWorkflow {
	return &PayoutWorkflow{base: base}
}

func (h *PayoutWorkflow) ServiceName() string { return "PayoutWorkflow" }

func (h *PayoutWorkflow) Run(
	ctx restate.WorkflowContext,
	input PayoutInput,
) (out PayoutOutput, err error) {
	defer metrics.TrackHandler("payout_workflow", "Run", &err)()

	out.OrderID = input.OrderID

	saga := saga.New(ctx)
	defer func() {
		if restate.IsTerminalError(err) {
			saga.Compensate()
		}
	}()

	// Step 1: open the seller-payout session + Pending payout tx. sessionID
	// equals the workflow key (= order ID) so DB row identity matches the
	// Restate workflow identity. payoutTxID is independent (multiple txs may
	// exist per session in future); pre-allocated via restate.UUID so it's
	// stable across replays — the INSERT becomes idempotent on PK conflict.
	sessionID := uuid.MustParse(restate.Key(ctx))
	payoutTxID := restate.UUID(ctx)

	// Compensator: mark the payout session + tx Failed if anything later in
	// the workflow terminally fails. Both queries guard on status='Pending'
	// so they no-op once the row reaches a final state — safe to retry.
	saga.Defer("mark_session_and_txs_failed", func(ctx restate.Context) error {
		return restate.RunVoid(ctx, func(rctx restate.RunContext) error {
			if _, e := h.base.storage.Querier().MarkPaymentSessionFailed(rctx, sessionID); e != nil {
				return fmt.Errorf("mark session failed: %w", e)
			}
			if e := h.base.storage.Querier().MarkTransactionsFailed(rctx, orderdb.MarkTransactionsFailedParams{
				ID:    []uuid.UUID{payoutTxID},
				Error: null.StringFrom("payout saga compensation"),
			}); e != nil {
				return fmt.Errorf("mark txs failed: %w", e)
			}
			return nil
		})
	})
	if err = restate.RunVoid(ctx, func(rctx restate.RunContext) error {
		if _, sErr := h.base.storage.Querier().CreateDefaultPaymentSession(
			rctx,
			orderdb.CreateDefaultPaymentSessionParams{
				ID:          sessionID,
				Kind:        ordermodel.SessionKindSellerPayout,
				Status:      orderdb.OrderStatusPending,
				FromID:      uuid.NullUUID{},
				ToID:        uuid.NullUUID{UUID: input.SellerID, Valid: true},
				Note:        "seller payout (escrow)",
				Currency:    input.Currency,
				TotalAmount: input.PaidTotal,
				Data:        json.RawMessage("{}"),
				DatePaid:    null.Time{},
				DateExpired: time.Now().Add(escrowWindow),
			}); sErr != nil {
			return fmt.Errorf("db create payout session: %w", sErr)
		}
		if _, txErr := h.base.storage.Querier().CreateDefaultTransaction(rctx, orderdb.CreateDefaultTransactionParams{
			ID:            payoutTxID,
			SessionID:     sessionID,
			Status:        orderdb.OrderStatusPending,
			Note:          "seller payout (escrow)",
			Error:         null.String{},
			PaymentOption: null.String{},
			Data:          json.RawMessage("{}"),
			Amount:        input.PaidTotal,
			Currency:      input.Currency,
			ReversesID:    uuid.NullUUID{},
			DateSettled:   null.Time{},
			DateExpired:   null.Time{},
		}); txErr != nil {
			return fmt.Errorf("db create payout tx: %w", txErr)
		}
		return nil
	}); err != nil {
		return out, fmt.Errorf("init payout session: %w", err)
	}

	deadline := time.Now().Add(escrowWindow)
	iter := 0

	for {
		// Reload the refund snapshot each iteration. It's tiny and journaling
		// it lets the next decision branch off a deterministic value.
		snap, snapErr := restate.Run(ctx, func(rctx restate.RunContext) (RefundSnapshot, error) {
			row, e := h.base.storage.Querier().GetRefundSnapshotByOrder(rctx, input.OrderID)
			if e != nil {
				return RefundSnapshot{}, e
			}
			return RefundSnapshot{
				HasActiveRefund:    row.HasActiveRefund,
				LastRefundApproved: row.LastRefundApproved,
			}, nil
		})
		if snapErr != nil {
			return out, fmt.Errorf("reload refund snapshot: %w", snapErr)
		}

		switch {
		case snap.LastRefundApproved:
			// A refund has settled — cancel the pending payout session/tx
			// and notify the seller. No wallet credit happens. Saga stays
			// armed: if MarkPaymentSessionCancelled fails terminal, the
			// compensator marks the still-Pending row Failed (better than
			// stuck Pending forever).
			if err = restate.RunVoid(ctx, func(rctx restate.RunContext) error {
				if _, e := h.base.storage.Querier().MarkPaymentSessionCancelled(rctx, sessionID); e != nil {
					return fmt.Errorf("mark payout session cancelled: %w", e)
				}
				return nil
			}); err != nil {
				return out, err
			}
			notifySellerPayoutCancelled(ctx, input.SellerID, input.OrderID)
			out.Outcome = "refunded"
			return out, nil

		case !snap.HasActiveRefund && !time.Now().Before(deadline):
			// Escrow window has elapsed with no active refund — release.
			// Each step is wrapped in restate.RunVoid so replays after a
			// crash use the journaled result instead of re-executing the
			// Pending-guarded UPDATE (which would fail with ErrNoRows on a
			// row already marked Success).
			if err = restate.RunVoid(ctx, func(rctx restate.RunContext) error {
				if _, e := h.base.storage.Querier().MarkPaymentSessionSuccess(rctx, orderdb.MarkPaymentSessionSuccessParams{
					ID: sessionID,
				}); e != nil {
					return fmt.Errorf("mark payout session success: %w", e)
				}
				if _, e := h.base.storage.Querier().MarkTransactionSuccess(rctx, orderdb.MarkTransactionSuccessParams{
					ID: payoutTxID,
				}); e != nil {
					return fmt.Errorf("mark payout tx success: %w", e)
				}
				return nil
			}); err != nil {
				return out, fmt.Errorf("mark payout success: %w", err)
			}
			// Saga stays armed (no Clear). The Pending-guarded compensator
			// no-ops on the now-Success rows, so a terminal failure of the
			// wallet credit below does NOT auto-revert the marks — operator
			// intervention is required for that gap.
			if err = h.base.account.WalletCredit(ctx, accountbiz.WalletCreditParams{
				AccountID: input.SellerID,
				Amount:    input.PaidTotal,
				Type:      "Payout",
				Reference: fmt.Sprintf("payout-session:%s", sessionID),
				Note:      "escrow released",
			}); err != nil {
				return out, fmt.Errorf("seller wallet credit: %w", err)
			}
			notifySellerPayoutReleased(ctx, input.SellerID, input.OrderID)
			out.Outcome = "released"
			return out, nil
		}

		// Wait for either a refund-state change (signalled by OnRefundChanged
		// against the iter-suffixed promise key) or the escrow deadline.
		iter++
		restate.Set(ctx, "refund_iter", iter)
		signal := restate.Promise[any](ctx, fmt.Sprintf("refund_changed_%d", iter))

		futs := []restate.Future{signal}
		if remaining := time.Until(deadline); remaining > 0 {
			futs = append(futs, restate.After(ctx, remaining))
		}
		// If both the signal hasn't fired and the deadline already passed,
		// loop immediately — the switch above will handle the release path.
		if len(futs) > 1 {
			if _, werr := restate.WaitFirst(ctx, futs...); werr != nil {
				return out, werr
			}
		} else {
			// Only the signal is left to wait on. This branch is hit when the
			// deadline has passed but a refund is still active, so we wait
			// indefinitely for the refund to settle one way or the other.
			if _, sErr := signal.Result(); sErr != nil {
				return out, sErr
			}
		}
	}
}

// OnRefundChanged is the shared-mode entry point invoked by the refund module
// every time a refund row for this order transitions state. It resolves the
// promise the Run loop is currently blocked on, identified by the iteration
// counter we persist in K/V state.
func (h *PayoutWorkflow) OnRefundChanged(
	ctx restate.WorkflowSharedContext,
	_ struct{},
) error {
	iter, err := restate.Get[int](ctx, "refund_iter")
	if err != nil {
		return fmt.Errorf("read refund_iter state: %w", err)
	}
	// iter == 0 means Run hasn't started its wait loop yet — the next
	// iteration's reload will pick up the change anyway, so no-op.
	if iter == 0 {
		return nil
	}
	return restate.Promise[any](ctx, fmt.Sprintf("refund_changed_%d", iter)).Resolve(nil)
}

func notifySellerPayoutReleased(ctx restate.WorkflowContext, sellerID, orderID uuid.UUID) {
	meta, _ := json.Marshal(map[string]string{"order_id": orderID.String()})
	restate.ServiceSend(ctx, "Account", "CreateNotification").Send(accountbiz.CreateNotificationParams{
		AccountID: sellerID,
		Type:      accountmodel.NotiPayoutReleased,
		Channel:   accountmodel.ChannelInApp,
		Title:     "Payout released",
		Content:   "Your escrow payout has been released to your wallet.",
		Metadata:  meta,
	})
}

func notifySellerPayoutCancelled(ctx restate.WorkflowContext, sellerID, orderID uuid.UUID) {
	meta, _ := json.Marshal(map[string]string{"order_id": orderID.String()})
	restate.ServiceSend(ctx, "Account", "CreateNotification").Send(accountbiz.CreateNotificationParams{
		AccountID: sellerID,
		Type:      accountmodel.NotiPayoutCancelled,
		Channel:   accountmodel.ChannelInApp,
		Title:     "Payout cancelled",
		Content:   "An approved refund has cancelled the escrow payout for this order.",
		Metadata:  meta,
	})
}

const escrowWindow = 7 * 24 * time.Hour

type PayoutInput struct {
	OrderID   uuid.UUID `json:"order_id"`
	SellerID  uuid.UUID `json:"seller_id"`
	PaidTotal int64     `json:"paid_total"`
	Currency  string    `json:"currency"`
}

type PayoutOutput struct {
	OrderID uuid.UUID `json:"order_id"`
	Outcome string    `json:"outcome"`
}

type RefundSnapshot struct {
	HasActiveRefund    bool `json:"has_active_refund"`
	LastRefundApproved bool `json:"last_refund_approved"`
}
