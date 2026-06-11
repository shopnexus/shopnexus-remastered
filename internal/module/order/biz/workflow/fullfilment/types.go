package fullfilment

import (
	restate "github.com/restatedev/sdk-go"

	accountmodel "shopnexus-server/internal/module/account/model"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"

	"github.com/google/uuid"
)

type FulfillmentInput struct {
	Account       accountmodel.AuthenticatedAccount
	ItemIDs       []int64 `validate:"required,min=1,max=1000"`
	UseWallet     bool
	WalletID      uuid.NullUUID
	PaymentOption string `validate:"max=100"`
	Note          string `validate:"max=500"`
}

type FulfillmentOutput struct {
	OrderID uuid.UUID `json:"order_id"`
	Outcome string    `json:"outcome"` // "released" | "refunded"
}

// confirmResult carries what the escrow phase needs from the confirm phase.
type confirmResult struct {
	SellerID  uuid.UUID `json:"seller_id"`
	PaidTotal int64     `json:"paid_total"`
	Currency  string    `json:"currency"`
}

// refundSnapshot is the journaled per-iteration projection of the order's
// refund state the escrow loop branches on. ActiveRefundID is uuid.Nil when
// no refund is active (COALESCEd in SQL).
type refundSnapshot struct {
	HasActiveRefund    bool      `json:"has_active_refund"`
	LastRefundApproved bool      `json:"last_refund_approved"`
	ActiveRefundID     uuid.UUID `json:"active_refund_id"`
}

// RefundCrediter is the slice of the refund handler the escrow loop drives on
// auto-accept. Declared here (not imported from the refund package) so the
// dependency points one way: fulfillment → refund handler, never the reverse.
// Exported so fx binds *refund.RefundHandler to it via fx.As.
type RefundCrediter interface {
	ExecuteRefundCredit(
		ctx restate.Context,
		refund orderdb.OrderRefund,
		deciderID uuid.UUID,
		reason ordermodel.RefundCreditReason,
	) (orderdb.OrderRefund, error)
}
