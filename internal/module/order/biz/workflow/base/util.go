package base

import (
	"context"
	"fmt"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
)

// MarkSessionAndAllPendingFailed is the saga-compensator body for multi-attempt
// sessions where individual gateway tx IDs aren't pre-allocated (each attempt
// generates its own). Marks the session + every still-Pending child tx as
// Failed by session_id. Idempotent on already-final rows.
func MarkSessionAndAllPendingFailed(
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
