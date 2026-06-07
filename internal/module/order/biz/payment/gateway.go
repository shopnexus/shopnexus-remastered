package payment

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	orderdb "shopnexus-server/internal/module/order/db/sqlc"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	restate "github.com/restatedev/sdk-go"
)

// GetReusableGatewayURL reports whether a checkout/confirm session has a
// Pending+not-expired gateway tx whose URL the client can reuse. The echo
// "ensure payment URL" handler uses this to skip a workflow round-trip on
// the happy path; on the retry path it falls back to RequestNewPaymentURL.
func (b *PaymentHandler) GetReusableGatewayURL(
	ctx restate.Context,
	sessionID uuid.UUID,
) (ReusableGatewayURLState, error) {
	var state ReusableGatewayURLState

	session, err := b.Storage.Querier().GetPaymentSession(ctx, uuid.NullUUID{UUID: sessionID, Valid: true})
	if err != nil {
		return state, fmt.Errorf("get payment session: %w", err)
	}
	if session.Status != orderdb.OrderStatusPending {
		state.SessionTerminated = true
		return state, nil
	}

	tx, err := b.Storage.Querier().GetLatestGatewayTxBySession(ctx, sessionID)
	if err != nil {
		// pgx returns ErrNoRows when no gateway tx exists yet — treat as
		// "no reusable URL" so the caller signals the workflow.
		if errors.Is(err, pgx.ErrNoRows) {
			return state, nil
		}
		return state, fmt.Errorf("get latest gateway tx: %w", err)
	}

	if tx.Status == orderdb.OrderStatusPending &&
		tx.DateExpired.Valid &&
		tx.DateExpired.Time.After(time.Now()) {
		var data struct {
			GatewayURL string `json:"gateway_url"`
		}
		if jerr := json.Unmarshal(tx.Data, &data); jerr == nil && data.GatewayURL != "" {
			state.ReusableURL = data.GatewayURL
		}
	}
	return state, nil
}

// ReusableGatewayURLState reports the latest gateway-payment state for a
// payment_session. SessionTerminated=true means the session is in a final
// state (Cancelled/Failed/Success) — caller should 410 Gone. ReusableURL
// non-empty means there's a Pending+not-expired tx; reuse it. Both empty
// means caller should signal the workflow to spawn the next attempt.
type ReusableGatewayURLState struct {
	SessionTerminated bool   `json:"session_terminated"`
	ReusableURL       string `json:"reusable_url"`
}
