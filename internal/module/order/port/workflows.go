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
)

// The three run inputs. Small structs with json tags: a run's input is stored by the runtime,
// so a field name here is a stored contract.
type CheckoutParams struct {
	SessionID int64 `json:"session_id"`
}

type OrderParams struct {
	OrderID int64 `json:"order_id"`
}

type RefundParams struct {
	RefundID int64 `json:"refund_id"`
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

	// StartOrder follows one order from creation to payout.
	StartOrder(ctx context.Context, orderID int64) error
	// OrderReceived is the buyer confirming the goods arrived, which starts the escrow
	// window; RefundRaised interrupts it and RefundResolved says whether the buyer was paid.
	OrderReceived(ctx context.Context, orderID int64) error
	RefundRaised(ctx context.Context, orderID int64) error
	RefundResolved(ctx context.Context, orderID int64, buyerPaid bool) error

	// StartRefund follows one case through its three windows; RefundMoved is any party
	// acting on it, so the run re-reads the row rather than being told what changed.
	StartRefund(ctx context.Context, refundID int64) error
	RefundMoved(ctx context.Context, refundID int64) error
}
