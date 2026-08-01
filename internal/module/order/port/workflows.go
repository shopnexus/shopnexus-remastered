package port

import "context"

// The workflow names the durable runtime knows this module by. Prefixed, because a Restate
// registry is shared across modules and "Order" alone is a name another one could want.
// They live here because the run's host and the code that submits it have to agree, and this
// is the only package both import.
const (
	CheckoutWorkflow = "OrderCheckout"
	OrderWorkflow    = "OrderLifecycle"
	RefundWorkflow   = "OrderRefund"
	OfferWorkflow    = "OrderOffer"
)

// The run inputs. Small structs with json tags: a run's input is stored by the runtime,
// so a field name here is a stored contract.
type CheckoutParams struct {
	SessionID int64 `json:"session_id"`
}

type OrderParams struct {
	OrderID int64 `json:"order_id"`
}

// OfferParams starts a run per negotiation. The run re-reads the row on every wake, so it does
// not need to know which of the two waits — unanswered proposal, or agreed terms nobody checked
// out — it is holding: both are the row's own deadline.
type OfferParams struct {
	OfferID int64 `json:"offer_id"`
}

// RefundParams is one window of one case: the refund, and the status whose clock the run is
// holding. The status is what makes the run safe to wake up — it advances the case only if it
// is still the state the run was started for, so a case that moved on has a run that returns
// rather than one that acts on somebody else's window.
type RefundParams struct {
	RefundID int64  `json:"refund_id"`
	Status   string `json:"status"`
}

// Workflows is the durable-timer side of this module: the waits nobody can hold in memory —
// an unpaid checkout expiring, an escrow window closing, a refund deadline passing.
//
// A seam rather than a direct dependency because there are two real deployments behind it: a
// runtime that holds a run per entity, and none at all, where the periodic sweep is the only
// clock. Both drive the same idempotent service methods, so neither is a second definition of
// "due".
//
// Every method is best-effort by contract: the caller has already committed the row that
// matters, so a runtime that is unreachable must not fail the request. The implementation
// returns its error; the service logs it and carries on.
type Workflows interface {
	// StartCheckout follows one payment session: the money, or the window closing on it.
	StartCheckout(ctx context.Context, sessionID int64) error
	// CheckoutPaid and CheckoutCancelled end that wait.
	CheckoutPaid(ctx context.Context, sessionID int64) error
	CheckoutCancelled(ctx context.Context, sessionID int64) error

	// StartOffer follows one negotiation to its deadline — a standing proposal nobody answered,
	// or agreed terms the buyer did not turn into an order. Both are the same wait on the same
	// row, which is why one run covers them.
	StartOffer(ctx context.Context, offerID int64) error

	// StartOrder follows one order from creation to payout.
	StartOrder(ctx context.Context, orderID int64) error
	// OrderReceived is the buyer confirming the goods arrived, which starts the escrow
	// window; RefundRaised interrupts it and RefundResolved says whether the buyer was paid.
	// OrderCancelled ends the run before any of that — the wait it is parked on is the receipt.
	OrderReceived(ctx context.Context, orderID int64) error
	OrderCancelled(ctx context.Context, orderID int64) error
	RefundRaised(ctx context.Context, orderID int64) error
	RefundResolved(ctx context.Context, orderID int64, buyerPaid bool) error

	// StartRefundWindow holds one window of one case, keyed by the refund and the status it
	// waits on. One run per window rather than one per case: a durable promise is
	// single-assignment per name, so a run that tried to reuse one wait for all three windows
	// spun instead of sleeping through the second.
	StartRefundWindow(ctx context.Context, refundID int64, status string) error
}
