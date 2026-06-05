package orderbiz

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"

	orderdb "shopnexus-server/internal/module/order/db/sqlc"

	"github.com/samber/lo"
)

// findOriginalCharge returns the session's original settled payment: the
// positive, Success transaction that is not itself a reversal. This is the
// single definition of "the buyer's charge" — the reversal target for refunds
// and the proof-of-payment for refund/cancel eligibility. ok=false means the
// session was never actually paid.
func findOriginalCharge(txs []orderdb.OrderTransaction) (orderdb.OrderTransaction, bool) {
	return lo.Find(txs, func(tx orderdb.OrderTransaction) bool {
		return tx.Status == orderdb.OrderStatusSuccess && tx.Amount > 0 && !tx.ReversesID.Valid
	})
}

// markSessionAndAllPendingFailed is the saga-compensator body for multi-attempt
// sessions where individual gateway tx IDs aren't pre-allocated (each attempt
// generates its own). Marks the session + every still-Pending child tx as
// Failed by session_id. Idempotent on already-final rows.
func markSessionAndAllPendingFailed(
	ctx context.Context,
	q orderdb.Querier,
	sessionID uuid.UUID,
	reason string,
) error {
	if _, e := q.MarkPaymentSessionFailed(ctx, sessionID); e != nil {
		return fmt.Errorf("mark session failed: %w", e)
	}
	if e := q.MarkPendingTxsFailedBySession(ctx, orderdb.MarkPendingTxsFailedBySessionParams{
		SessionID: sessionID,
		Error:     null.StringFrom(reason),
	}); e != nil {
		return fmt.Errorf("mark txs failed by session: %w", e)
	}
	return nil
}
