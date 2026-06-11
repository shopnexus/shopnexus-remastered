package ordermodel

import (
	orderdb "shopnexus-server/internal/module/order/db/sqlc"

	"github.com/google/uuid"
	"github.com/samber/lo"
)

// FindOriginalCharge returns the session's original settled payment: the
// positive, Success transaction that is not itself a reversal. This is the
// single definition of "the buyer's charge" — the reversal target for refunds
// and the proof-of-payment for refund/cancel eligibility. ok=false means the
// session was never actually paid.
func FindOriginalCharge(txs []orderdb.OrderTransaction) (orderdb.OrderTransaction, bool) {
	return lo.Find(txs, func(tx orderdb.OrderTransaction) bool {
		return tx.Status == orderdb.OrderStatusSuccess && tx.Amount > 0 && !tx.ReversesID.Valid
	})
}

// RefundSignal identifies which of the order's refunds a workflow signal is
// about. One fulfillment workflow instance sees every refund the order
// accumulates, so refund-scoped promises are namespaced by this ID.
type RefundSignal struct {
	RefundID uuid.UUID `json:"refund_id"`
}

// SellerDecisionSignal is sent by SellerApproveRefund / SellerDisputeRefund.
type SellerDecisionSignal struct {
	RefundID uuid.UUID `json:"refund_id"`
	Approved bool      `json:"approved"`
}

// AdminDecisionSignal is sent by AdminUpholdDispute / AdminDismissDispute.
type AdminDecisionSignal struct {
	RefundID uuid.UUID `json:"refund_id"`
	Upheld   bool      `json:"upheld"`
}

// TransportDeliveredSignal is sent by the real transport webhook when a
// return shipment is physically delivered.
type TransportDeliveredSignal struct {
	RefundID uuid.UUID `json:"refund_id"`
}

type CreditFromSessionParams struct {
	SessionID  uuid.UUID
	AccountID  uuid.UUID
	CreditType string
	Reference  string
	Note       string
}

type RefundCreditReason string

const (
	RefundCreditReasonSellerApproved RefundCreditReason = "seller-approved"
	RefundCreditReasonAutoAccepted   RefundCreditReason = "auto-accepted (seller silent)"
	RefundCreditReasonAdminDismissed RefundCreditReason = "admin-dismissed dispute"
)
