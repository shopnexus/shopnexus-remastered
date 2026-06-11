package fullfilment

import (
	"encoding/json"
	"fmt"
	"time"

	restate "github.com/restatedev/sdk-go"

	accountbiz "shopnexus-server/internal/module/account/biz"
	accountmodel "shopnexus-server/internal/module/account/model"
	"shopnexus-server/internal/module/order/biz/workflow/gateway"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/shared/saga"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
)

// returnDeliveredKey is the promise the real transport webhook resolves when a
// buyer's return shipment is physically delivered. Keyed by refund ID because
// one workflow instance sees every refund the order accumulates.
func returnDeliveredKey(refundID uuid.UUID) string { return "return_delivered_" + refundID.String() }

// escrow opens the seller-payout session and watches the order until the
// escrow window elapses (release) or a refund is accepted (refunded). Active
// refunds are resolved inline before the next decision.
func (h *FulfillmentWorkflow) escrow(
	ctx restate.WorkflowContext,
	sg *saga.Saga,
	orderID uuid.UUID,
	conf confirmResult,
) (outcome string, err error) {
	// IDs pre-allocated via restate.UUID: stable across replays, INSERTs
	// idempotent on PK conflict. The payout session gets its own UUID — the
	// confirm session already owns the order-ID value in payment_session.
	sessionID := restate.UUID(ctx)
	payoutTxID := restate.UUID(ctx)

	// Journal the deadline so replays keep the original window.
	deadline, err := restate.Run(ctx, func(restate.RunContext) (time.Time, error) {
		return time.Now().Add(escrowWindow), nil
	})
	if err != nil {
		return "", fmt.Errorf("journal escrow deadline: %w", err)
	}

	// Compensator: mark the payout session + still-Pending txs Failed if
	// anything later terminally fails. Pending-guarded → no-ops on rows that
	// reached a final state, so it stays armed for the rest of the workflow.
	sg.Defer("mark_payout_failed", func(ctx restate.Context) error {
		return restate.RunVoid(ctx, func(rctx restate.RunContext) error {
			return gateway.MarkSessionFailed(rctx, h.Storage.Querier(), sessionID, "payout saga compensation")
		})
	})
	if err = restate.RunVoid(ctx, func(rctx restate.RunContext) error {
		if _, sErr := h.Storage.Querier().CreateDefaultPaymentSession(rctx, orderdb.CreateDefaultPaymentSessionParams{
			ID:          sessionID,
			Kind:        ordermodel.SessionKindSellerPayout,
			Status:      orderdb.OrderStatusPending,
			FromID:      uuid.NullUUID{},
			ToID:        uuid.NullUUID{UUID: conf.SellerID, Valid: true},
			Note:        "seller payout (escrow)",
			Currency:    conf.Currency,
			TotalAmount: conf.PaidTotal,
			Data:        json.RawMessage("{}"),
			DatePaid:    null.Time{},
			DateExpired: deadline,
		}); sErr != nil {
			return fmt.Errorf("db create payout session: %w", sErr)
		}
		if _, txErr := h.Storage.Querier().CreateDefaultTransaction(rctx, orderdb.CreateDefaultTransactionParams{
			ID:        payoutTxID,
			SessionID: sessionID,
			Status:    orderdb.OrderStatusPending,
			Note:      "seller payout (escrow)",
			Data:      json.RawMessage("{}"),
			Amount:    conf.PaidTotal,
			Currency:  conf.Currency,
		}); txErr != nil {
			return fmt.Errorf("db create payout tx: %w", txErr)
		}
		return nil
	}); err != nil {
		return "", fmt.Errorf("init payout session: %w", err)
	}

	iter := 0
	for {
		// Journaled refund snapshot — the decision below branches off a
		// deterministic value.
		snap, snapErr := restate.Run(ctx, func(rctx restate.RunContext) (refundSnapshot, error) {
			row, e := h.Storage.Querier().GetRefundSnapshotByOrder(rctx, orderID)
			if e != nil {
				return refundSnapshot{}, e
			}
			return refundSnapshot{
				HasActiveRefund:    row.HasActiveRefund,
				LastRefundApproved: row.LastRefundApproved,
				ActiveRefundID:     row.ActiveRefundID,
			}, nil
		})
		if snapErr != nil {
			return "", fmt.Errorf("reload refund snapshot: %w", snapErr)
		}

		switch {
		case snap.LastRefundApproved:
			// A refund settled — cancel the pending payout. No wallet credit.
			// The armed compensator marks the row Failed if this terminally
			// fails (better than stuck Pending forever).
			if err = restate.RunVoid(ctx, func(rctx restate.RunContext) error {
				_, e := h.Storage.Querier().MarkPaymentSessionCancelled(rctx, sessionID)
				return e
			}); err != nil {
				return "", fmt.Errorf("mark payout session cancelled: %w", err)
			}
			if err = h.NotifyOrder(ctx, conf.SellerID, orderID, accountmodel.NotiPayoutCancelled,
				"Payout cancelled", "An approved refund has cancelled the escrow payout for this order."); err != nil {
				return "", fmt.Errorf("notify payout cancelled: %w", err)
			}
			return "refunded", nil

		case snap.HasActiveRefund && snap.ActiveRefundID != uuid.Nil:
			// Drive the active refund to a terminal state, then re-snapshot.
			if err = h.resolveRefund(ctx, snap.ActiveRefundID); err != nil {
				return "", fmt.Errorf("resolve refund: %w", err)
			}
			continue

		case !time.Now().Before(deadline):
			// Window elapsed, no refund in flight — release. Wrapped in RunVoid
			// so replays use the journaled result instead of re-executing the
			// Pending-guarded UPDATEs (which would fail with ErrNoRows on rows
			// already marked Success).
			if err = restate.RunVoid(ctx, func(rctx restate.RunContext) error {
				if _, e := h.Storage.Querier().MarkPaymentSessionSuccess(rctx, orderdb.MarkPaymentSessionSuccessParams{
					ID: sessionID,
				}); e != nil {
					return fmt.Errorf("mark payout session success: %w", e)
				}
				if _, e := h.Storage.Querier().MarkTransactionSuccess(rctx, orderdb.MarkTransactionSuccessParams{
					ID: payoutTxID,
				}); e != nil {
					return fmt.Errorf("mark payout tx success: %w", e)
				}
				return nil
			}); err != nil {
				return "", fmt.Errorf("mark payout success: %w", err)
			}
			// Saga stays armed (no Clear). The Pending-guarded compensator
			// no-ops on the now-Success rows, so a terminal failure of the
			// wallet credit below does NOT auto-revert the marks — operator
			// intervention is required for that gap.
			if err = h.account.Call().WalletCredit(ctx, accountbiz.WalletCreditParams{
				AccountID: conf.SellerID,
				Amount:    conf.PaidTotal,
				Type:      "Payout",
				Reference: fmt.Sprintf("payout-session:%s", sessionID),
				Note:      "escrow released",
			}); err != nil {
				return "", fmt.Errorf("seller wallet credit: %w", err)
			}
			if err = h.NotifyOrder(ctx, conf.SellerID, orderID, accountmodel.NotiPayoutReleased,
				"Payout released", "Your escrow payout has been released to your wallet."); err != nil {
				return "", fmt.Errorf("notify payout released: %w", err)
			}
			return "released", nil
		}

		// Idle: wait for a refund-state change (signalled by OnRefundChanged
		// against the iter-suffixed promise key) or the escrow deadline. Only
		// reachable while the deadline is in the future — a passed deadline with
		// no active refund hits the release case above.
		iter++
		restate.Set(ctx, "refund_iter", iter)
		signal := restate.Promise[any](ctx, fmt.Sprintf("refund_changed_%d", iter))
		if _, err = restate.WaitFirst(ctx, signal, restate.After(ctx, time.Until(deadline))); err != nil {
			return "", fmt.Errorf("wait refund signal: %w", err)
		}
	}
}

// resolveRefund drives one refund to a terminal state (withdrawn, accepted or
// rejected — all recorded on the refund row by the biz handlers it calls).
// Promises are suffixed with the refund ID because one workflow instance sees
// every refund the order accumulates.
func (h *FulfillmentWorkflow) resolveRefund(ctx restate.WorkflowContext, refundID uuid.UUID) error {
	var err error

	// Phase 1: race {buyer withdraw | return delivered | shipping timeout}.
	// returnDelivered is resolved by the real transport webhook (OnTransportDelivered);
	// the webhook also marks the return-transport row Success, so no DB flip here.
	withdrawn := restate.Promise[any](ctx, "withdrawn_"+refundID.String())
	returnDelivered := restate.Promise[any](ctx, returnDeliveredKey(refundID))
	shippingDeadline := restate.After(ctx, forwardTransportTimeout)

	winner, err := restate.WaitFirst(ctx, withdrawn, returnDelivered, shippingDeadline)
	if err != nil {
		return fmt.Errorf("wait return transport: %w", err)
	}
	switch winner {
	case withdrawn:
		// Row already flipped to Cancelled by WithdrawBuyerRefund.
		return nil
	case shippingDeadline:
		// Carrier lost the package — platform eats the loss, buyer gets credit.
		if err = h.autoAcceptRefund(ctx, refundID); err != nil {
			return fmt.Errorf("auto-accept on shipping timeout: %w", err)
		}
		return nil
	}

	// returnDelivered → flip refund row + arm the seller review window.
	if err = h.markRefundDelivered(ctx, refundID); err != nil {
		return fmt.Errorf("mark delivered: %w", err)
	}

	// Phase 2: race {seller decision | auto-accept after review window}.
	decision := restate.Promise[ordermodel.SellerDecisionSignal](ctx, "seller_decision_"+refundID.String())
	reviewDeadline := restate.After(ctx, sellerReviewWindow)
	winner, err = restate.WaitFirst(ctx, decision, reviewDeadline)
	if err != nil {
		return fmt.Errorf("wait seller decision: %w", err)
	}
	if winner == reviewDeadline {
		if err = h.autoAcceptRefund(ctx, refundID); err != nil {
			return fmt.Errorf("auto-accept on review timeout: %w", err)
		}
		return nil
	}
	sellerDecision, err := decision.Result()
	if err != nil {
		return fmt.Errorf("read seller decision: %w", err)
	}
	if sellerDecision.Approved {
		// Credit already executed by SellerApproveRefund.
		return nil
	}

	// Phase 3: disputed → admin verdict (no SLA timer; manual resolution). The
	// dispute handlers update the refund row; the snapshot decides next.
	if _, err = restate.Promise[ordermodel.AdminDecisionSignal](
		ctx,
		"admin_decision_"+refundID.String(),
	).Result(); err != nil {
		return fmt.Errorf("wait admin decision: %w", err)
	}
	return nil
}
