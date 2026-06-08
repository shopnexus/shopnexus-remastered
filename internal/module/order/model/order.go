package ordermodel

import (
	commonmodel "shopnexus-server/internal/module/common/model"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"

	"github.com/google/uuid"
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
	Session orderdb.OrderPaymentSession `json:"session"`
	Items   []CheckoutSummaryItem       `json:"items"`
}

// OrderItem is the domain-layer item (pre- and post-confirmation).
// Refund status is derived from negative-amount transactions in the item's payment session.
type OrderItem struct {
	orderdb.OrderItem

	// Hydrated from catalog so the FE can render product cards without
	// fanning out: slug for the product link, image for the thumbnail.
	Slug     string `json:"slug"`
	ImageURL string `json:"image_url"`

	// Derived (optional loaded):
	PaymentSession *orderdb.OrderPaymentSession `json:"payment_session,omitempty"`
}

// Order is the domain-layer confirmed order (exists only after seller confirm).
type Order struct {
	orderdb.OrderOrder

	// Derived (optional loaded):
	TotalAmount    int64                        `json:"total_amount"`
	Items          []OrderItem                  `json:"items"`
	Transport      *orderdb.OrderTransport      `json:"transport"`
	ConfirmSession *orderdb.OrderPaymentSession `json:"confirm_session"`
	PayoutSession  *orderdb.OrderPaymentSession `json:"payout_session"`
}

// Refund is the v2 refund request. Buyer ships physical return at create
// time; seller decides within 3 days of delivery or auto-accept fires.
// Resources carries the buyer's evidence photos (common resource system).
type Refund struct {
	orderdb.OrderRefund

	Resources []commonmodel.Resource `json:"resources"`
}

// RefundDispute is the seller-initiated escalation against a refund.
// Resources carries the seller's evidence photos (common resource system).
type RefundDispute struct {
	orderdb.OrderRefundDispute

	Resources []commonmodel.Resource `json:"resources"`
}

// SessionKind values mirror the strings stored in payment_session.kind.
// The DB column is plain TEXT (not an enum) — these constants are the source of truth.
const (
	SessionKindBuyerCheckout         = "buyer-checkout"
	SessionKindSellerConfirmationFee = "seller-confirmation-fee"
	SessionKindSellerPayout          = "seller-payout"
)
