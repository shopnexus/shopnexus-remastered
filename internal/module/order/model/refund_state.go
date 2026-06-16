package ordermodel

import "github.com/samber/lo"

// refundTransitions is the refund FSM: the single source of truth for legal
// status moves. Terminal states (Accepted/Rejected/Cancelled) are intentionally
// absent — IsTerminal keys off that absence.
//
//nolint:exhaustive // terminal states have no outgoing edges by design
var refundTransitions = map[RefundStatus][]RefundStatus{
	RefundStatusShipping: {
		RefundStatusAwaitingSellerReview,
		RefundStatusCancelled,
	},
	RefundStatusAwaitingSellerReview: {
		RefundStatusAccepted,
		RefundStatusDisputed,
	},
	RefundStatusDisputed: {
		RefundStatusAccepted,
		RefundStatusRejected,
	},
}

// CanTransitionTo reports whether next is a legal successor of the current state.
func (r Refund) CanTransitionTo(next RefundStatus) bool {
	return lo.Contains(refundTransitions[r.Status], next)
}

// CanSellerDecide reports whether the seller may approve or dispute now: only
// after the return is delivered and before any decision is recorded.
func (r Refund) CanSellerDecide() bool {
	return r.Status == RefundStatusAwaitingSellerReview
}

// CanWithdraw reports whether the buyer may still withdraw: only while the
// return is in transit, before the seller takes possession.
func (r Refund) CanWithdraw() bool {
	return r.Status == RefundStatusShipping
}

// IsTerminal reports whether the refund has reached a final state.
func (r Refund) IsTerminal() bool {
	_, hasOutgoing := refundTransitions[r.Status]
	return !hasOutgoing
}
