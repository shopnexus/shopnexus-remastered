package ordermodel

import (
	"encoding/json"
	"time"

	commonmodel "shopnexus-server/internal/module/common/model"

	"github.com/google/uuid"
	null "github.com/guregu/null/v6"
)

// CheckoutSummaryItem is the projection of an OrderItem with the catalog data
// the payment-result page needs to render: product name, primary image, qty,
// per-line totals.
type CheckoutSummaryItem struct {
	ID          int64     `json:"id"`
	SkuID       uuid.UUID `json:"sku_id"`
	SpuID       uuid.UUID `json:"spu_id"`
	Slug        string    `json:"slug"`
	SkuName     string    `json:"sku_name"`
	Quantity    int64     `json:"quantity"`
	TotalAmount int64     `json:"total_amount"`
	Currency    string    `json:"currency"`
	ImageURL    string    `json:"image_url"`
}

// CheckoutSummary is the bundle returned to the payment-result page so it can
// show what the user just paid for without the FE making N hop calls.
type CheckoutSummary struct {
	Session PaymentSession        `json:"session"`
	Items   []CheckoutSummaryItem `json:"items"`
}

//pgtempl:table "order"."order"
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

	// derived (no db tag):
	TotalAmount    int64           `json:"total_amount"`
	Items          []OrderItem     `json:"items"`
	Transport      *Transport      `json:"transport"`
	ConfirmSession *PaymentSession `json:"confirm_session"`
	PayoutSession  *PaymentSession `json:"payout_session"`
}

//pgtempl:table "order"."item"
type OrderItem struct {
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

	// derived (no db tag):
	Slug           string          `json:"slug"`
	ImageURL       string          `json:"image_url"`
	PaymentSession *PaymentSession `json:"payment_session,omitempty"`
}

//pgtempl:table "order"."refund"
type Refund struct {
	ID                       uuid.UUID     `db:"id"`
	AccountID                uuid.UUID     `db:"account_id"`
	OrderID                  uuid.UUID     `db:"order_id"`
	Reason                   string        `db:"reason"`
	DateCreated              time.Time     `db:"date_created"`
	Status                   RefundStatus  `db:"status"`
	ReturnTransportID        int64         `db:"return_transport_id"`
	DateReceivedBySeller     null.Time     `db:"date_received_by_seller"`
	ReviewDeadline           null.Time     `db:"review_deadline"`
	SellerDecisionAt         null.Time     `db:"seller_decision_at"`
	ReturnToBuyerTransportID null.Int      `db:"return_to_buyer_transport_id"`
	RejectionReason          null.String   `db:"rejection_reason"`
	RefundTxID               uuid.NullUUID `db:"refund_tx_id"`

	// derived (no db tag):
	Resources []commonmodel.Resource `json:"resources"`
}

//pgtempl:table "order"."refund_dispute"
type RefundDispute struct {
	ID             uuid.UUID     `db:"id"`
	RefundID       uuid.UUID     `db:"refund_id"`
	AccountID      uuid.UUID     `db:"account_id"`
	Reason         string        `db:"reason"`
	DateCreated    time.Time     `db:"date_created"`
	Status         DisputeStatus `db:"status"`
	ResolvedByID   uuid.NullUUID `db:"resolved_by_id"`
	DateResolved   null.Time     `db:"date_resolved"`
	ResolutionNote null.String   `db:"resolution_note"`

	// derived (no db tag):
	Resources []commonmodel.Resource `json:"resources"`
}

// SessionKind values mirror the strings stored in payment_session.kind.
// The DB column is plain TEXT (not an enum) — these constants are the source of truth.
const (
	SessionKindBuyerCheckout         = "buyer-checkout"
	SessionKindSellerConfirmationFee = "seller-confirmation-fee"
	SessionKindSellerPayout          = "seller-payout"
)
