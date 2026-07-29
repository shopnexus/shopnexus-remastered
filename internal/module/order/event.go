package order

import "shopnexus/internal/infra/eventbus"

// OrderPlaced is published on the bus after an order is persisted.
type OrderPlaced struct {
	// Raw keys: this never leaves the process boundary as a public payload, and
	// the consuming module wants the database key anyway.
	OrderID int64 `json:"order_id"`
	BuyerID int64 `json:"buyer_id"`
	Total   int64 `json:"total"`
}

// OrderPlacedTopic is the bus topic carrying OrderPlaced events.
var OrderPlacedTopic = eventbus.NewTopic[OrderPlaced]("order.placed")
