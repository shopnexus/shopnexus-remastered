package ordermodel

import (
	"time"

	"github.com/google/uuid"
)

// SellerOrderStats is the aggregate stats row for a seller over a date range.
type SellerOrderStats struct {
	TotalRevenue int64 `json:"total_revenue"`
	TotalOrders  int64 `json:"total_orders"`
	ItemsSold    int64 `json:"items_sold"`
}

// SellerOrderTimePoint is one time-bucketed revenue/count row.
type SellerOrderTimePoint struct {
	Bucket     time.Time `json:"bucket"`
	Revenue    int64     `json:"revenue"`
	OrderCount int64     `json:"order_count"`
}

// SellerPendingActions holds unconfirmed-items and pending-refund counts.
type SellerPendingActions struct {
	PendingItems   int64 `json:"pending_items"`
	PendingRefunds int64 `json:"pending_refunds"`
}

// SellerTopProduct is one product row ranked by sold quantity.
type SellerTopProduct struct {
	SkuID     uuid.UUID `json:"sku_id"`
	SkuName   string    `json:"sku_name"`
	SoldCount int64     `json:"sold_count"`
	Revenue   int64     `json:"revenue"`
}

// TransportWithOrder embeds Transport and adds the three joined order fields.
type TransportWithOrder struct {
	Transport

	OrderID       uuid.UUID `json:"order_id"`
	OrderBuyerID  uuid.UUID `json:"order_buyer_id"`
	OrderSellerID uuid.UUID `json:"order_seller_id"`
}
