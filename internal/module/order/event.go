package order

import (
	"context"

	"shopnexus/internal/infra/eventbus"
	financedomain "shopnexus/internal/module/finance/domain"
)

// OrderPlaced is published after an order is written and its escrow held. A fact, not an
// instruction: nothing downstream is required to act on it, and the order is already real
// whether or not anybody does.
type OrderPlaced struct {
	// Raw keys: this never leaves the process as a public payload, and a consumer wants the
	// database key anyway.
	OrderID  int64  `json:"order_id"`
	BuyerID  int64  `json:"buyer_id"`
	SellerID int64  `json:"seller_id"`
	Total    int64  `json:"total"`
	Currency string `json:"currency"`
}

// OrderPlacedTopic carries OrderPlaced. Declared once here, so nothing else names the string.
var OrderPlacedTopic = eventbus.NewTopic[OrderPlaced]("order.placed")

func publishOrderPlaced(ctx context.Context, bus eventbus.Client, event OrderPlaced) error {
	return eventbus.Publish(ctx, bus, OrderPlacedTopic, event)
}

// financeMovementPosted is the error finance answers when an idempotency key has already
// been used. Named here because the lifecycle treats it as success: a movement that was
// already posted is the state the caller wanted.
var financeMovementPosted = financedomain.ErrMovementAlreadyPosted
