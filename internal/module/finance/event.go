package finance

import (
	"context"
	"encoding/json/jsontext"

	"shopnexus/internal/infra/eventbus"
	"shopnexus/internal/module/finance/domain"
)

// SessionPaid is published when a payment session is fully covered. It is the fact
// the rest of the platform waits on: order creates the order from it, because the
// money landing is what makes a sale — nobody confirms anything in between.
// KindBuyerCheckout is re-exported at the level a subscriber imports: order compares
// SessionPaid.Kind against it, and it may not reach into this module's domain package. One
// declaration, so renaming the kind cannot quietly stop turning paid sessions into orders.
const KindBuyerCheckout = domain.KindBuyerCheckout

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
	Data jsontext.Value `json:"data,omitempty"`
}

// SessionPaidTopic carries SessionPaid. The name is the code a subscriber names, so it
// is declared once here and nowhere else.
var SessionPaidTopic = eventbus.NewTopic[SessionPaid]("finance.session_paid")

func publishSessionPaid(ctx context.Context, bus eventbus.Client, event SessionPaid) error {
	return eventbus.Publish(ctx, bus, SessionPaidTopic, event)
}

// SessionCancelled is published when a payer walks away from a session before the money
// moves. The counterpart of SessionPaid, and needed for the same reason: whoever opened
// the session reserved something against it, and cancelling the money without saying so
// leaves that reservation held by a session nobody can pay any more.
//
// Cancelling used to be a write and nothing else. A buyer who dropped a checkout kept the
// stock reserved and kept the lines in their own pending list — offering to pay for a
// session the server would refuse — until the checkout window ran out hours later.
type SessionCancelled struct {
	// Raw keys, like SessionPaid: this never leaves the process as a public payload.
	SessionID int64  `json:"session_id"`
	Kind      string `json:"kind"`
	FromID    int64  `json:"from_id"`
	ToID      int64  `json:"to_id"`
}

// SessionCancelledTopic carries SessionCancelled.
var SessionCancelledTopic = eventbus.NewTopic[SessionCancelled]("finance.session_cancelled")

func publishSessionCancelled(ctx context.Context, bus eventbus.Client, event SessionCancelled) error {
	return eventbus.Publish(ctx, bus, SessionCancelledTopic, event)
}
