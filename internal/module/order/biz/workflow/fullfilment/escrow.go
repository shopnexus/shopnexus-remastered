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

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
)

// returnDeliveredKey keys by refundID because one workflow instance sees every refund the order accumulates.
func returnDeliveredKey(refundID uuid.UUID) string { return "return_delivered_" + refundID.String() }

// escrow watches the order until the escrow window elapses (release) or a refund is accepted (refunded).
func (r *fulfillmentRun) escrow() (outcome string, err error) {
	ctx := r.ctx
	// restate.UUID is journaled — stable across replays, INSERTs idempotent on PK conflict.
	sessionID := restate.UUID(ctx)
	payoutTxID := restate.UUID(ctx)

	// Journaled so replays keep the original deadline.
	deadline, err := restate.Run(ctx, func(restate.RunContext) (time.Time, error) {
		return time.Now().Add(escrowWindow), nil
	})
	if err != nil {
		return "", fmt.Errorf("journal escrow deadline: %w", err)
	}

	// open payout session: create the Pending payout session + tx (+ fail compensator)
	// Pending-guarded → no-ops on already-final rows; stays armed for the whole workflow.
	r.sg.Defer("mark_payout_failed", func(ctx restate.Context) error {
		return restate.RunVoid(ctx, func(rctx restate.RunContext) error {
			return gateway.MarkSessionFailed(rctx, r.Storage.Querier(), sessionID, "payout saga compensation")
		})
	})
	if err = restate.RunVoid(ctx, func(rctx restate.RunContext) error {
		if _, sErr := r.Storage.Querier().CreateDefaultPaymentSession(rctx, orderdb.CreateDefaultPaymentSessionParams{
			ID:          sessionID,
			Kind:        ordermodel.SessionKindSellerPayout,
			Status:      orderdb.OrderStatusPending,
			FromID:      uuid.NullUUID{},
			ToID:        uuid.NullUUID{UUID: r.conf.SellerID, Valid: true},
			Note:        "seller payout (escrow)",
			Currency:    r.conf.Currency,
			TotalAmount: r.conf.PaidTotal,
			Data:        json.RawMessage("{}"),
			DatePaid:    null.Time{},
			DateExpired: deadline,
		}); sErr != nil {
			return fmt.Errorf("db create payout session: %w", sErr)
		}
		if _, txErr := r.Storage.Querier().CreateDefaultTransaction(rctx, orderdb.CreateDefaultTransactionParams{
			ID:        payoutTxID,
			SessionID: sessionID,
			Status:    orderdb.OrderStatusPending,
			Note:      "seller payout (escrow)",
			Data:      json.RawMessage("{}"),
			Amount:    r.conf.PaidTotal,
			Currency:  r.conf.Currency,
		}); txErr != nil {
			return fmt.Errorf("db create payout tx: %w", txErr)
		}
		return nil
	}); err != nil {
		return "", fmt.Errorf("init payout session: %w", err)
	}

	// watch loop: reload refund snapshot, branch, else wait for a signal or the deadline
	iter := 0
	for {
		snap, snapErr := restate.Run(ctx, func(rctx restate.RunContext) (refundSnapshot, error) {
			row, e := r.Storage.Querier().GetRefundSnapshotByOrder(rctx, r.orderID)
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
		// refunded: an approved refund cancels the payout
		case snap.LastRefundApproved:
			if err = restate.RunVoid(ctx, func(rctx restate.RunContext) error {
				_, e := r.Storage.Querier().MarkPaymentSessionCancelled(rctx, sessionID)
				return e
			}); err != nil {
				return "", fmt.Errorf("mark payout session cancelled: %w", err)
			}
			if err = r.NotifyOrder(ctx, r.conf.SellerID, r.orderID, accountmodel.NotiPayoutCancelled,
				"Payout cancelled", "An approved refund has cancelled the escrow payout for this order."); err != nil {
				return "", fmt.Errorf("notify payout cancelled: %w", err)
			}
			return "refunded", nil

		// resolve active refund: drive it to terminal, then re-evaluate
		case snap.HasActiveRefund && snap.ActiveRefundID != uuid.Nil:
			if err = r.resolveRefund(snap.ActiveRefundID); err != nil {
				return "", fmt.Errorf("resolve refund: %w", err)
			}
			continue

		// release: deadline passed → mark success, credit seller, notify
		case !time.Now().Before(deadline):
			// Wrapped in RunVoid so replays use the journaled result; re-executing the
			// Pending-guarded UPDATEs would fail with ErrNoRows on already-Success rows.
			if err = restate.RunVoid(ctx, func(rctx restate.RunContext) error {
				if _, e := r.Storage.Querier().MarkPaymentSessionSuccess(rctx, orderdb.MarkPaymentSessionSuccessParams{
					ID: sessionID,
				}); e != nil {
					return fmt.Errorf("mark payout session success: %w", e)
				}
				if _, e := r.Storage.Querier().MarkTransactionSuccess(rctx, orderdb.MarkTransactionSuccessParams{
					ID: payoutTxID,
				}); e != nil {
					return fmt.Errorf("mark payout tx success: %w", e)
				}
				return nil
			}); err != nil {
				return "", fmt.Errorf("mark payout success: %w", err)
			}
			// Saga stays armed; compensator no-ops on Success rows, so a terminal credit failure
			// leaves marks in place and requires operator intervention.
			if err = r.account.Call().WalletCredit(ctx, accountbiz.WalletCreditParams{
				AccountID: r.conf.SellerID,
				Amount:    r.conf.PaidTotal,
				Type:      "Payout",
				Reference: fmt.Sprintf("payout-session:%s", sessionID),
				Note:      "escrow released",
			}); err != nil {
				return "", fmt.Errorf("seller wallet credit: %w", err)
			}
			if err = r.NotifyOrder(ctx, r.conf.SellerID, r.orderID, accountmodel.NotiPayoutReleased,
				"Payout released", "Your escrow payout has been released to your wallet."); err != nil {
				return "", fmt.Errorf("notify payout released: %w", err)
			}
			return "released", nil
		}

		// idle wait: block on a refund-state change or the escrow deadline.
		iter++
		restate.Set(ctx, "refund_iter", iter)
		signal := restate.Promise[any](ctx, fmt.Sprintf("refund_changed_%d", iter))
		if _, err = restate.WaitFirst(ctx, signal, restate.After(ctx, time.Until(deadline))); err != nil {
			return "", fmt.Errorf("wait refund signal: %w", err)
		}
	}
}

// resolveRefund drives one refund to terminal state. Promise keys are suffixed with refundID
// because one workflow instance sees every refund the order accumulates.
func (r *fulfillmentRun) resolveRefund(refundID uuid.UUID) error {
	ctx := r.ctx
	var err error

	// Phase 1: race {buyer withdraw | return delivered | shipping timeout}
	withdrawn := restate.Promise[any](ctx, "withdrawn_"+refundID.String())
	returnDelivered := restate.Promise[any](ctx, returnDeliveredKey(refundID))
	shippingDeadline := restate.After(ctx, forwardTransportTimeout)

	winner, err := restate.WaitFirst(ctx, withdrawn, returnDelivered, shippingDeadline)
	if err != nil {
		return fmt.Errorf("wait return transport: %w", err)
	}
	switch winner {
	case withdrawn:
		return nil
	case shippingDeadline:
		// Carrier lost the package — platform eats the loss, auto-accept for the buyer.
		if err = r.autoAcceptRefund(refundID); err != nil {
			return fmt.Errorf("auto-accept on shipping timeout: %w", err)
		}
		return nil
	}

	if err = r.markRefundDelivered(refundID); err != nil {
		return fmt.Errorf("mark delivered: %w", err)
	}

	// Phase 2: race {seller decision | review window expiry}
	decision := restate.Promise[ordermodel.SellerDecisionSignal](ctx, "seller_decision_"+refundID.String())
	reviewDeadline := restate.After(ctx, sellerReviewWindow)
	winner, err = restate.WaitFirst(ctx, decision, reviewDeadline)
	if err != nil {
		return fmt.Errorf("wait seller decision: %w", err)
	}
	if winner == reviewDeadline {
		if err = r.autoAcceptRefund(refundID); err != nil {
			return fmt.Errorf("auto-accept on review timeout: %w", err)
		}
		return nil
	}
	sellerDecision, err := decision.Result()
	if err != nil {
		return fmt.Errorf("read seller decision: %w", err)
	}
	if sellerDecision.Approved {
		return nil
	}

	// Phase 3: disputed → admin verdict (manual resolution, no SLA timer).
	if _, err = restate.Promise[ordermodel.AdminDecisionSignal](
		ctx,
		"admin_decision_"+refundID.String(),
	).Result(); err != nil {
		return fmt.Errorf("wait admin decision: %w", err)
	}
	return nil
}
