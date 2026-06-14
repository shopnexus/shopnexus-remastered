package fullfilment

import (
	"fmt"
	"time"

	restate "github.com/restatedev/sdk-go"

	"shopnexus-server/internal/infras/locker"
	"shopnexus-server/internal/infras/metrics"
	accountbiz "shopnexus-server/internal/module/account/biz"
	orderbase "shopnexus-server/internal/module/order/biz/base"
	"shopnexus-server/internal/module/order/biz/workflow/gateway"
	"shopnexus-server/internal/shared/saga"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
)

const (
	// escrowWindow is how long the seller payout sits in escrow before release.
	escrowWindow = 7 * 24 * time.Hour
	// sellerReviewWindow is how long the seller has from physical delivery to
	// decide accept/dispute before the refund auto-accepts in the buyer's favor.
	sellerReviewWindow = 3 * 24 * time.Hour
	// forwardTransportTimeout caps how long we wait for the return-transport
	// webhook to fire. After this the refund auto-accepts — defends against lost
	// packages (the platform eats the loss rather than stranding the buyer).
	forwardTransportTimeout = 14 * 24 * time.Hour
)

// FulfillmentWorkflow drives one order's full lifecycle: confirm-fee saga → order creation → escrow.
// Workflow key = confirm session ID = order ID — all signals route by it.
type FulfillmentWorkflow struct {
	*orderbase.Base

	gw      *gateway.Gateway
	account accountbiz.AccountBizClient
	locker  locker.Client
	refund  RefundCrediter
}

func NewFulfillmentWorkflow(
	core *orderbase.Base,
	gw *gateway.Gateway,
	account accountbiz.AccountBizClient,
	locker locker.Client,
	refund RefundCrediter,
) *FulfillmentWorkflow {
	return &FulfillmentWorkflow{core, gw, account, locker, refund}
}

func (h *FulfillmentWorkflow) ServiceName() string { return "FulfillmentWorkflow" }

// fulfillmentRun is the per-invocation scope; phases share state via its fields.
type fulfillmentRun struct {
	*FulfillmentWorkflow
	ctx     restate.WorkflowContext
	sg      *saga.Saga
	input   FulfillmentInput
	orderID uuid.UUID

	conf confirmResult // set by confirm()
}

func (h *FulfillmentWorkflow) Run(
	ctx restate.WorkflowContext,
	input FulfillmentInput,
) (FulfillmentOutput, error) {
	var err error
	var zero FulfillmentOutput
	defer metrics.TrackHandler("fulfillment_workflow", "Run", &err)()

	if err = validator.Validate(input); err != nil {
		return zero, fmt.Errorf("validate fulfillment: %w", err)
	}

	r := &fulfillmentRun{
		FulfillmentWorkflow: h,
		ctx:                 ctx,
		sg:                  saga.New(ctx),
		input:               input,
		orderID:             uuid.MustParse(restate.Key(ctx)),
	}
	// Unblock any synchronous GetPaymentURL caller on terminal failure.
	r.sg.Defer("reject_payment_url", func(_ restate.Context) error {
		return r.gw.RejectPendingURLs(ctx, err)
	})
	defer func() {
		if restate.IsTerminalError(err) {
			r.sg.Compensate()
		}
	}()

	// confirm: seller confirm-fee saga → order creation
	if err = r.confirm(); err != nil {
		return zero, fmt.Errorf("confirm fulfillment: %w", err)
	}

	// escrow: hold payout until release or refund
	outcome, err := r.escrow()
	if err != nil {
		return zero, fmt.Errorf("escrow fulfillment: %w", err)
	}

	return FulfillmentOutput{
		Outcome: outcome,
		OrderID: r.orderID,
	}, err
}
