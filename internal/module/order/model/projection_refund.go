package ordermodel

import "github.com/google/uuid"

// RefundSnapshot is the per-iteration projection the fulfillment workflow reads
// while watching escrow.
type RefundSnapshot struct {
	// HasActiveRefund is true while any refund for the order is in negotiation.
	HasActiveRefund bool
	// LastRefundApproved is true once the most-recent refund row lands in Accepted.
	LastRefundApproved bool
	// ActiveRefundID is the refund the workflow must resolve inline; zero UUID when none.
	ActiveRefundID uuid.UUID
}
