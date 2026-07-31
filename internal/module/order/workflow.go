package order

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	restate "github.com/restatedev/sdk-go"

	orderapi "shopnexus/internal/module/order/api"
	"shopnexus/internal/module/order/domain"
	"shopnexus/internal/module/order/port"
	"shopnexus/internal/shared/id"
)

// Lifecycle is the durable side of this module: the waits nobody can hold in memory. Restate
// journals each step and each timer, so a restart resumes where the run left off rather than
// starting the clock again — which is why none of these methods keeps state of its own and
// every effect they call is idempotent.
//
// A workflow instance is keyed by the entity it follows. The signals resolve promises; the
// Run method is the state machine, forward-only, one phase per wait.
type Lifecycle struct{ svc *Service }

func NewLifecycle(svc orderapi.Service) *Lifecycle {
	// The workflow drives this module's own service; anything else in the graph would be a
	// different module's business.
	own, ok := svc.(*Service)
	if !ok {
		return nil
	}
	return &Lifecycle{svc: own}
}

// The promises each workflow waits on. Named constants because a signal and its wait have to
// agree, and a typo'd literal would wait forever.
const (
	promPaid      = "paid"
	promCancelled = "cancelled"
	promReceived  = "received"
	promRefunded  = "refund-raised"
	promResolved  = "refund-resolved"
)

// The three run inputs live in port, because the code that submits a run and the code that
// hosts it have to agree and that is the only package both import.

// Checkout follows one payment session: it waits for the money, and closes the session's
// lines if nothing arrives.
//
// The timer is the point. A checkout that is never paid has to release the stock it reserved,
// and a durable timer does that without a table of pending expiries for a cron to sweep.
type Checkout struct{ l *Lifecycle }

func (l *Lifecycle) Checkout() *Checkout { return &Checkout{l: l} }

// ServiceName is what the runtime registers this workflow as. Explicit rather than the
// struct's name, because the registry is shared across modules and "Checkout" alone is a name
// another one could want.
func (w *Checkout) ServiceName() string { return port.CheckoutWorkflow }

func (w *Checkout) Run(ctx restate.WorkflowContext, p port.CheckoutParams) error {
	paid := restate.Promise[bool](ctx, promPaid)
	cancelled := restate.Promise[bool](ctx, promCancelled)
	// Race the money against the session's own window. Whichever resolves first decides;
	// the timer is durable, so a restart does not restart the clock.
	winner, err := restate.WaitFirst(ctx, paid, cancelled, restate.After(ctx, checkoutWindow))
	if err != nil {
		return fmt.Errorf("wait for payment: %w", err)
	}
	if winner != paid {
		// Nothing arrived, or the buyer walked away: the lines go and the stock with them.
		return restate.RunVoid(ctx, func(rctx restate.RunContext) error {
			return w.l.cancelSessionLines(rctx, p.SessionID)
		}, restate.WithName("cancelUnpaidLines"))
	}
	// The money landed. Writing the order is idempotent, so a retried step is not a second
	// order — the origin's unique constraint is what says so.
	return restate.RunVoid(ctx, func(rctx restate.RunContext) error {
		return w.l.svc.SettlePaidSession(rctx, id.Of[id.PaymentSession](p.SessionID))
	}, restate.WithName("settlePaidSession"))
}

// ConfirmPaid is finance's signal: the session is covered.
func (w *Checkout) ConfirmPaid(ctx restate.WorkflowSharedContext) error {
	return restate.Promise[bool](ctx, promPaid).Resolve(true)
}

// Cancelled is the buyer walking away before paying.
func (w *Checkout) Cancelled(ctx restate.WorkflowSharedContext) error {
	return restate.Promise[bool](ctx, promCancelled).Resolve(true)
}

// Order follows one order from creation to payout: delivery, then the escrow window, with a
// refund able to interrupt it.
type Order struct{ l *Lifecycle }

func (l *Lifecycle) Order() *Order { return &Order{l: l} }

func (w *Order) ServiceName() string { return port.OrderWorkflow }

func (w *Order) Run(ctx restate.WorkflowContext, p port.OrderParams) error {
	// Phase one has no timeout on purpose: how long delivery takes is the carrier's and the
	// seller's business, and a clock here would cancel an order that is merely slow.
	if _, err := restate.Promise[bool](ctx, promReceived).Result(); err != nil {
		return fmt.Errorf("await receipt: %w", err)
	}

	// Phase two: the buyer's window to raise a refund, raced against the escrow release.
	refunded := restate.Promise[bool](ctx, promRefunded)
	winner, err := restate.WaitFirst(ctx, refunded, restate.After(ctx, domain.PayoutWindow))
	if err != nil {
		return fmt.Errorf("wait escrow window: %w", err)
	}
	if winner == refunded {
		// A refund is its own run with its own windows; this one waits for the verdict and
		// only pays out if the seller kept the money.
		resolved, err := restate.Promise[bool](ctx, promResolved).Result()
		if err != nil {
			return fmt.Errorf("await refund resolution: %w", err)
		}
		if resolved {
			// The buyer was paid back, so there is nothing to release.
			return nil
		}
	}
	return restate.RunVoid(ctx, func(rctx restate.RunContext) error {
		_, err := w.l.svc.ReleaseDuePayouts(rctx, 1)
		return err
	}, restate.WithName("releasePayout"))
}

// Received is the buyer confirming the goods arrived.
func (w *Order) Received(ctx restate.WorkflowSharedContext) error {
	return restate.Promise[bool](ctx, promReceived).Resolve(true)
}

// RefundRaised interrupts the escrow window.
func (w *Order) RefundRaised(ctx restate.WorkflowSharedContext) error {
	return restate.Promise[bool](ctx, promRefunded).Resolve(true)
}

// RefundResolved carries the verdict: true when the buyer was paid back, so the payout does
// not also happen.
func (w *Order) RefundResolved(ctx restate.WorkflowSharedContext, buyerPaid bool) error {
	return restate.Promise[bool](ctx, promResolved).Resolve(buyerPaid)
}

// Refund follows one case through its three windows. Each phase is a race between somebody
// acting and the clock running out, which is exactly what the deadline column says — the
// workflow holds the timer, the row holds the state.
type Refund struct{ l *Lifecycle }

func (l *Lifecycle) Refund() *Refund { return &Refund{l: l} }

func (w *Refund) ServiceName() string { return port.RefundWorkflow }

func (w *Refund) Run(ctx restate.WorkflowContext, p port.RefundParams) error {
	// One loop rather than three phases: every non-terminal status names the party it waits
	// on and carries their deadline, so "wait, then advance" is the same step each time.
	for {
		refund, err := restate.Run(ctx, func(rctx restate.RunContext) (domain.Refund, error) {
			return w.l.refund(rctx, p.RefundID)
		}, restate.WithName("readRefund"))
		if err != nil {
			return fmt.Errorf("read refund: %w", err)
		}
		if refund.Settled() {
			return nil
		}
		if refund.DeadlineAt == nil {
			// A carrier or a moderator decides this one; a timer would be guessing. The run
			// waits for the signal that says the state moved.
			if _, err := restate.Promise[bool](ctx, promResolved).Result(); err != nil {
				return fmt.Errorf("await refund move: %w", err)
			}
			continue
		}
		moved := restate.Promise[bool](ctx, promResolved)
		wait := time.Until(*refund.DeadlineAt)
		if wait < 0 {
			wait = 0
		}
		winner, err := restate.WaitFirst(ctx, moved, restate.After(ctx, wait))
		if err != nil {
			return fmt.Errorf("wait refund deadline: %w", err)
		}
		if winner == moved {
			continue
		}
		// The clock ran out on whoever the status named.
		if err := restate.RunVoid(ctx, func(rctx restate.RunContext) error {
			_, err := w.l.svc.AdvanceOverdueRefunds(rctx, 1)
			return err
		}, restate.WithName("advanceOverdueRefund")); err != nil {
			return err
		}
	}
}

// Moved is any party acting on the refund: the run re-reads the row and waits on the new
// deadline rather than trying to know what changed.
func (w *Refund) Moved(ctx restate.WorkflowSharedContext) error {
	return restate.Promise[bool](ctx, promResolved).Resolve(true)
}

// checkoutWindow mirrors the payment session's own expiry: the workflow must not outlive the
// thing it is waiting for.
const checkoutWindow = 15 * time.Minute

// refund reads one case, which is all the workflow needs to know whose clock is running.
func (l *Lifecycle) refund(ctx context.Context, refundID int64) (domain.Refund, error) {
	return l.svc.repo.FindRefund(ctx, refundID)
}

// cancelSessionLines closes the lines of a session nobody paid and gives the stock back. The
// per-line guard makes it idempotent: a line already cancelled is skipped, so a retried timer
// releases nothing twice.
func (l *Lifecycle) cancelSessionLines(ctx context.Context, sessionID int64) error {
	items, err := l.svc.repo.ItemsByPaymentSession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("read session items: %w", err)
	}
	for _, i := range items {
		if !i.Live() || i.OrderID != nil {
			continue
		}
		if _, err := l.svc.CancelItem(ctx, orderapi.ItemRequest{
			ActorID: id.Of[id.Account](i.BuyerID), ID: id.Of[id.Item](i.ID),
		}); err != nil {
			l.svc.log.Error("cancel unpaid line", "item_id", i.ID, "err", err)
		}
	}
	return nil
}

// Definitions are the three workflows for a runtime to serve. Reflect turns each exported
// method with a workflow context into a handler, so the signal names the submitter uses are
// the method names here — that pairing is the only thing keeping them in step.
func (l *Lifecycle) Definitions() []restate.ServiceDefinition {
	return []restate.ServiceDefinition{
		restate.Reflect(l.Checkout()),
		restate.Reflect(l.Order()),
		restate.Reflect(l.Refund()),
	}
}

// Sweep is what a deployment without a durable runtime falls back on, and the net under a
// lost run when there is one: the same
// the same transitions, driven by a plain interval. Kept as one place so the sweep and the
// workflows cannot drift into two different definitions of "due".
func (l *Lifecycle) Sweep(ctx context.Context, log *slog.Logger) {
	if closed, err := l.svc.ExpireDrafts(ctx, 100); err != nil {
		log.Error("expire drafts", "err", err)
	} else if closed > 0 {
		log.Info("expired drafts", "count", closed)
	}
	if closed, err := l.svc.ExpireOffers(ctx, 100); err != nil {
		log.Error("expire offers", "err", err)
	} else if closed > 0 {
		log.Info("expired offers", "count", closed)
	}
	if paid, err := l.svc.ReleaseDuePayouts(ctx, 100); err != nil {
		log.Error("release payouts", "err", err)
	} else if paid > 0 {
		log.Info("released payouts", "count", paid)
	}
	if moved, err := l.svc.AdvanceOverdueRefunds(ctx, 100); err != nil {
		log.Error("advance refunds", "err", err)
	} else if moved > 0 {
		log.Info("advanced refunds", "count", moved)
	}
}
