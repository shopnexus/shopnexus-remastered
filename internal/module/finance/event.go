package finance

import (
	"context"
	"encoding/json"

	"shopnexus/internal/infra/eventbus"
)

// SessionPaid is published when a payment session is fully covered. It is the fact
// the rest of the platform waits on: order creates the order from it, because the
// money landing is what makes a sale — nobody confirms anything in between.
type SessionPaid struct {
	// Raw keys: this never leaves the process as a public payload, and the consuming
	// module wants the database key anyway.
	SessionID int64  `json:"session_id"`
	Kind      string `json:"kind"`
	FromID    int64  `json:"from_id"`
	ToID      int64  `json:"to_id"`
	Currency  string `json:"currency"`
	Amount    int64  `json:"amount"`
	// Data is the checkout context the opener stored — which draft or offer the sale
	// came from — handed back so the consumer needs no second lookup.
	Data json.RawMessage `json:"data,omitempty"`
}

// SessionPaidTopic carries SessionPaid. The name is the code a subscriber names, so it
// is declared once here and nowhere else.
var SessionPaidTopic = eventbus.NewTopic[SessionPaid]("finance.session_paid")

func publishSessionPaid(ctx context.Context, bus eventbus.Client, event SessionPaid) error {
	return eventbus.Publish(ctx, bus, SessionPaidTopic, event)
}
