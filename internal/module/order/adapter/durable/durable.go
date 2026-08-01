// Package durable implements the order module's port.Workflows over a durable-execution
// runtime — and, when none is configured, over nothing at all.
//
// Two implementations because both are real deployments: with Restate a run per entity holds
// each wait and is signalled the moment the state moves; without it the periodic sweep is the
// only clock. Neither adds a rule the other lacks, which is what keeps "due" defined once.
package durable

import (
	"context"
	"log/slog"
	"strconv"

	restate "github.com/restatedev/sdk-go"

	infra "shopnexus/internal/infra/durable"
	"shopnexus/internal/module/order/port"
)

// The signal handlers, which have to agree with the method names on the workflow structs.
const (
	signalPaid         = "ConfirmPaid"
	signalCancelled    = "Cancelled"
	signalReceived     = "Received"
	signalRefundRaised = "RefundRaised"
	signalRefundDone   = "RefundResolved"
)

// Restate starts and signals runs through the ingress.
type Restate struct {
	client *infra.Client
}

func New(client *infra.Client) *Restate { return &Restate{client: client} }

var _ port.Workflows = (*Restate)(nil)

func (r *Restate) StartCheckout(ctx context.Context, sessionID int64) error {
	return r.client.Start(ctx, infra.Run{
		Workflow: port.CheckoutWorkflow, Key: key(sessionID),
		Input: port.CheckoutParams{SessionID: sessionID},
	})
}

func (r *Restate) CheckoutPaid(ctx context.Context, sessionID int64) error {
	return r.signal(ctx, port.CheckoutWorkflow, sessionID, signalPaid, nil)
}

func (r *Restate) CheckoutCancelled(ctx context.Context, sessionID int64) error {
	return r.signal(ctx, port.CheckoutWorkflow, sessionID, signalCancelled, nil)
}

func (r *Restate) StartOrder(ctx context.Context, orderID int64) error {
	return r.client.Start(ctx, infra.Run{
		Workflow: port.OrderWorkflow, Key: key(orderID), Input: port.OrderParams{OrderID: orderID},
	})
}

func (r *Restate) OrderReceived(ctx context.Context, orderID int64) error {
	return r.signal(ctx, port.OrderWorkflow, orderID, signalReceived, nil)
}

func (r *Restate) OrderCancelled(ctx context.Context, orderID int64) error {
	return r.signal(ctx, port.OrderWorkflow, orderID, signalCancelled, nil)
}

func (r *Restate) RefundRaised(ctx context.Context, orderID int64) error {
	return r.signal(ctx, port.OrderWorkflow, orderID, signalRefundRaised, nil)
}

func (r *Restate) RefundResolved(ctx context.Context, orderID int64, buyerPaid bool) error {
	return r.signal(ctx, port.OrderWorkflow, orderID, signalRefundDone, buyerPaid)
}

// StartRefundWindow keys the run by the refund *and* the status it waits on, so entering the
// next state starts a run of its own rather than re-attaching to a journal that has already
// resolved its wait.
func (r *Restate) StartRefundWindow(ctx context.Context, refundID int64, status string) error {
	return r.client.Start(ctx, infra.Run{
		Workflow: port.RefundWorkflow,
		Key:      key(refundID) + ":" + status,
		Input:    port.RefundParams{RefundID: refundID, Status: status},
	})
}

func (r *Restate) signal(ctx context.Context, workflow string, id int64, handler string, input any) error {
	if input == nil {
		input = restate.Void{}
	}
	return r.client.Signal(ctx, infra.Signal{
		Workflow: workflow, Key: key(id), Handler: handler, Input: input,
	})
}

// key is the entity's raw id as the workflow key. Raw rather than opaque: the key is
// internal to the runtime, and a permuted one would make a stuck run impossible to find
// from the row it follows.
func key(id int64) string { return strconv.FormatInt(id, 10) }

// Off is the no-runtime deployment: every wait is left to the sweep. It logs at debug so a
// misconfiguration shows up as "the timers are the slow ones" rather than as silence.
type Off struct{ log *slog.Logger }

func NewOff(log *slog.Logger) *Off { return &Off{log: log} }

var _ port.Workflows = (*Off)(nil)

func (o *Off) StartCheckout(_ context.Context, sessionID int64) error {
	return o.skip("checkout", sessionID)
}

func (o *Off) CheckoutPaid(_ context.Context, sessionID int64) error {
	return o.skip("checkout paid", sessionID)
}

func (o *Off) CheckoutCancelled(_ context.Context, sessionID int64) error {
	return o.skip("checkout cancelled", sessionID)
}

func (o *Off) StartOrder(_ context.Context, orderID int64) error {
	return o.skip("order", orderID)
}

func (o *Off) OrderReceived(_ context.Context, orderID int64) error {
	return o.skip("order received", orderID)
}

func (o *Off) OrderCancelled(_ context.Context, orderID int64) error {
	return o.skip("order cancelled", orderID)
}

func (o *Off) RefundRaised(_ context.Context, orderID int64) error {
	return o.skip("refund raised", orderID)
}

func (o *Off) RefundResolved(_ context.Context, orderID int64, _ bool) error {
	return o.skip("refund resolved", orderID)
}

func (o *Off) StartRefundWindow(_ context.Context, refundID int64, status string) error {
	return o.skip("refund window "+status, refundID)
}

func (o *Off) skip(what string, id int64) error {
	o.log.Debug("no workflow runtime; the sweep will pick this up", "what", what, "id", id)
	return nil
}
