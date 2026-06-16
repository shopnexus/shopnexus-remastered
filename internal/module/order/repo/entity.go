package orderrepo

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	null "github.com/guregu/null/v6"

	orderdb "shopnexus-server/internal/module/order/db/sqlc"
)

// Order is "order"."order".
type Order struct {
	ID               uuid.UUID   `db:"id"`
	BuyerID          uuid.UUID   `db:"buyer_id"`
	SellerID         uuid.UUID   `db:"seller_id"`
	TransportID      int64       `db:"transport_id"`
	Address          string      `db:"address"`
	DateCreated      time.Time   `db:"date_created"`
	ConfirmedByID    uuid.UUID   `db:"confirmed_by_id"`
	ConfirmSessionID uuid.UUID   `db:"confirm_session_id"`
	Note             null.String `db:"note"`
}

// Item is "order"."item".
type Item struct {
	ID               int64           `db:"id"`
	OrderID          uuid.NullUUID   `db:"order_id"`
	AccountID        uuid.UUID       `db:"account_id"`
	SellerID         uuid.UUID       `db:"seller_id"`
	SkuID            uuid.UUID       `db:"sku_id"`
	SpuID            uuid.UUID       `db:"spu_id"`
	SkuName          string          `db:"sku_name"`
	Address          string          `db:"address"`
	Note             null.String     `db:"note"`
	SerialIds        json.RawMessage `db:"serial_ids"`
	Quantity         int64           `db:"quantity"`
	TransportOption  string          `db:"transport_option"`
	SubtotalAmount   int64           `db:"subtotal_amount"`
	TotalAmount      int64           `db:"total_amount"`
	SourceCurrency   string          `db:"source_currency"`
	PaymentSessionID uuid.UUID       `db:"payment_session_id"`
	DateCancelled    null.Time       `db:"date_cancelled"`
	CancelledByID    uuid.NullUUID   `db:"cancelled_by_id"`
	DateCreated      time.Time       `db:"date_created"`
}

// CartItem is "order"."cart_item".
type CartItem struct {
	ID        int64     `db:"id"`
	AccountID uuid.UUID `db:"account_id"`
	SkuID     uuid.UUID `db:"sku_id"`
	Quantity  int64     `db:"quantity"`
}

// PaymentSession is "order"."payment_session".
type PaymentSession struct {
	ID          uuid.UUID           `db:"id"`
	Kind        string              `db:"kind"`
	Status      orderdb.OrderStatus `db:"status"`
	FromID      uuid.NullUUID       `db:"from_id"`
	ToID        uuid.NullUUID       `db:"to_id"`
	Note        string              `db:"note"`
	Currency    string              `db:"currency"`
	TotalAmount int64               `db:"total_amount"`
	FxSnapshot  json.RawMessage     `db:"fx_snapshot"`
	Data        json.RawMessage     `db:"data"`
	DateCreated time.Time           `db:"date_created"`
	DatePaid    null.Time           `db:"date_paid"`
	DateExpired time.Time           `db:"date_expired"`
}

// Transaction is "order"."transaction".
type Transaction struct {
	ID            uuid.UUID           `db:"id"`
	SessionID     uuid.UUID           `db:"session_id"`
	Status        orderdb.OrderStatus `db:"status"`
	Note          string              `db:"note"`
	Error         null.String         `db:"error"`
	PaymentOption null.String         `db:"payment_option"`
	Data          json.RawMessage     `db:"data"`
	Amount        int64               `db:"amount"`
	Currency      string              `db:"currency"`
	ReversesID    uuid.NullUUID       `db:"reverses_id"`
	DateCreated   time.Time           `db:"date_created"`
	DateSettled   null.Time           `db:"date_settled"`
	DateExpired   null.Time           `db:"date_expired"`
}

// Transport is "order"."transport".
type Transport struct {
	ID          int64                   `db:"id"`
	Option      string                  `db:"option"`
	Status      orderdb.NullOrderStatus `db:"status"`
	Data        json.RawMessage         `db:"data"`
	DateCreated time.Time               `db:"date_created"`
}

// Refund is "order"."refund".
type Refund struct {
	ID                       uuid.UUID                 `db:"id"`
	AccountID                uuid.UUID                 `db:"account_id"`
	OrderID                  uuid.UUID                 `db:"order_id"`
	Reason                   string                    `db:"reason"`
	DateCreated              time.Time                 `db:"date_created"`
	Status                   orderdb.OrderRefundStatus `db:"status"`
	ReturnTransportID        int64                     `db:"return_transport_id"`
	DateReceivedBySeller     null.Time                 `db:"date_received_by_seller"`
	ReviewDeadline           null.Time                 `db:"review_deadline"`
	SellerDecisionAt         null.Time                 `db:"seller_decision_at"`
	ReturnToBuyerTransportID null.Int                  `db:"return_to_buyer_transport_id"`
	RejectionReason          null.String               `db:"rejection_reason"`
	RefundTxID               uuid.NullUUID             `db:"refund_tx_id"`
}

// RefundDispute is "order"."refund_dispute".
type RefundDispute struct {
	ID             uuid.UUID                  `db:"id"`
	RefundID       uuid.UUID                  `db:"refund_id"`
	AccountID      uuid.UUID                  `db:"account_id"`
	Reason         string                     `db:"reason"`
	DateCreated    time.Time                  `db:"date_created"`
	Status         orderdb.OrderDisputeStatus `db:"status"`
	ResolvedByID   uuid.NullUUID              `db:"resolved_by_id"`
	DateResolved   null.Time                  `db:"date_resolved"`
	ResolutionNote null.String                `db:"resolution_note"`
}
