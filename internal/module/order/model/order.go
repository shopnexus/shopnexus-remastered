package ordermodel

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
)

// PaymentSession is the domain-layer payment intent: one logical money flow
// (checkout, confirmation fee, payout). Has 0..N child Transaction rail movements.
type PaymentSession struct {
	ID          uuid.UUID       `json:"id"`
	Kind        string          `json:"kind"`
	Status      Status          `json:"status"`
	FromID      uuid.NullUUID   `json:"from_id"`
	ToID        uuid.NullUUID   `json:"to_id"`
	Note        string          `json:"note"`
	Currency    string          `json:"currency"`
	TotalAmount int64           `json:"total_amount"`
	FxSnapshot  json.RawMessage `json:"fx_snapshot,omitempty"`
	Data        json.RawMessage `json:"data"`

	DateCreated time.Time `json:"date_created"`
	DatePaid    null.Time `json:"date_paid"`
	DateExpired time.Time `json:"date_expired"`
}

// Transaction is the domain-layer ledger leg: one rail movement within a payment session.
// Reversals are NEW rows with negative amount + ReversesID pointing to the original.
type Transaction struct {
	ID            uuid.UUID       `json:"id"`
	SessionID     uuid.UUID       `json:"session_id"`
	Status        Status          `json:"status"`
	Note          string          `json:"note"`
	Error         null.String     `json:"error"`
	PaymentOption null.String     `json:"payment_option"`
	Data          json.RawMessage `json:"data"`

	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`

	ReversesID uuid.NullUUID `json:"reverses_id"`

	DateCreated time.Time `json:"date_created"`
	DateSettled null.Time `json:"date_settled"`
	DateExpired null.Time `json:"date_expired"`
}

// Transport is the domain-layer representation of a shipping record.
// Status is serialized as a plain string ("" when DB row had NULL) so FE
// consumers can compare against enum values directly instead of unwrapping
// the {order_status, valid} sqlc null wrapper.
type Transport struct {
	ID          int64           `json:"id"`
	OptionID    string          `json:"option_id"`
	Status      Status          `json:"status"`
	Data        json.RawMessage `json:"data"`
	DateCreated time.Time       `json:"date_created"`
}

// CheckoutSummaryItem is the projection of an OrderItem with the catalog data
// the payment-result page needs to render: product name, primary image, qty,
// per-line totals. Lightweight on purpose — the result page is read-only.
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

// OrderItem is the domain-layer item (pre- and post-confirmation).
// Refund status is derived from negative-amount transactions in the item's payment session.
type OrderItem struct {
	ID        int64           `json:"id"`
	OrderID   uuid.NullUUID   `json:"order_id"`
	AccountID uuid.UUID       `json:"account_id"`
	SellerID  uuid.UUID       `json:"seller_id"`
	SkuID     uuid.UUID       `json:"sku_id"`
	SpuID     uuid.UUID       `json:"spu_id"`
	SkuName   string          `json:"sku_name"`
	Address   string          `json:"address"`
	Note      null.String     `json:"note"`
	SerialIDs json.RawMessage `json:"serial_ids"`

	Quantity         int64     `json:"quantity"`
	TransportOption  string    `json:"transport_option"`
	SubtotalAmount   int64     `json:"subtotal_amount"`
	TotalAmount      int64     `json:"total_amount"`
	SourceCurrency   string    `json:"source_currency"`
	PaymentSessionID uuid.UUID `json:"payment_session_id"`

	DateCreated   time.Time     `json:"date_created"`
	DateCancelled null.Time     `json:"date_cancelled"`
	CancelledByID uuid.NullUUID `json:"cancelled_by_id"`

	// Hydrated from catalog so the FE can render product cards without
	// fanning out: slug for the product link, image for the thumbnail.
	Slug     string `json:"slug"`
	ImageURL string `json:"image_url"`

	// Derived (optional loaded):
	PaymentSession *PaymentSession `json:"payment_session,omitempty"`
}

// Order is the domain-layer confirmed order (exists only after seller confirm).
type Order struct {
	ID          uuid.UUID `json:"id"`
	BuyerID     uuid.UUID `json:"buyer_id"`
	SellerID    uuid.UUID `json:"seller_id"`
	TransportID int64     `json:"transport_id"`
	Address     string    `json:"address"`
	DateCreated time.Time `json:"date_created"`

	ConfirmedByID    uuid.UUID   `json:"confirmed_by_id"`
	ConfirmSessionID uuid.UUID   `json:"confirm_session_id"`
	Note             null.String `json:"note"`

	// Derived (optional loaded):
	TotalAmount    int64           `json:"total_amount"`
	Items          []OrderItem     `json:"items"`
	Transport      *Transport      `json:"transport,omitempty"`
	ConfirmSession *PaymentSession `json:"confirm_session,omitempty"`
	PayoutSession  *PaymentSession `json:"payout_session,omitempty"`
}

// Refund is the v2 refund request. Buyer ships physical return at create
// time; seller decides within 3 days of delivery or auto-accept fires.
type Refund struct {
	ID          uuid.UUID       `json:"id"`
	AccountID   uuid.UUID       `json:"account_id"`
	OrderID     uuid.UUID       `json:"order_id"`
	Reason      string          `json:"reason"`
	Attachments json.RawMessage `json:"attachments"`
	DateCreated time.Time       `json:"date_created"`
	Status      RefundStatus    `json:"status"`

	ReturnTransportID    int64     `json:"return_transport_id"`
	DateReceivedBySeller null.Time `json:"date_received_by_seller"`
	ReviewDeadline       null.Time `json:"review_deadline"`

	SellerDecisionAt null.Time `json:"seller_decision_at"`

	ReturnToBuyerTransportID null.Int    `json:"return_to_buyer_transport_id"`
	RejectionReason          null.String `json:"rejection_reason"`

	RefundTxID uuid.NullUUID `json:"refund_tx_id"`
}

// RefundDispute is the seller-initiated escalation against a refund.
type RefundDispute struct {
	ID             uuid.UUID       `json:"id"`
	RefundID       uuid.UUID       `json:"refund_id"`
	AccountID      uuid.UUID       `json:"account_id"`
	Reason         string          `json:"reason"`
	Attachments    json.RawMessage `json:"attachments"`
	Status         DisputeStatus   `json:"status"`
	DateCreated    time.Time       `json:"date_created"`
	ResolvedByID   uuid.NullUUID   `json:"resolved_by_id"`
	DateResolved   null.Time       `json:"date_resolved"`
	ResolutionNote null.String     `json:"resolution_note"`
}

// SessionKind values mirror the strings stored in payment_session.kind.
// The DB column is plain TEXT (not an enum) — these constants are the source of truth.
const (
	SessionKindBuyerCheckout         = "buyer-checkout"
	SessionKindSellerConfirmationFee = "seller-confirmation-fee"
	SessionKindSellerPayout          = "seller-payout"
)
