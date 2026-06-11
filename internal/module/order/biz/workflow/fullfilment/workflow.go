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

// FulfillmentWorkflow drives one order's full lifecycle: seller confirm-fee
// saga → order creation → escrow watch → payout release or refund. The
// workflow key, the confirm session ID and the order ID are the same UUID,
// minted at HTTP submission time — the payment webhook, the FE retry/cancel
// endpoints and every refund/dispute signal all route by it.
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

func (h *FulfillmentWorkflow) Run(
	ctx restate.WorkflowContext,
	input FulfillmentInput,
) (out FulfillmentOutput, err error) {
	defer metrics.TrackHandler("fulfillment_workflow", "Run", &err)()

	orderID := uuid.MustParse(restate.Key(ctx))

	if err = validator.Validate(input); err != nil {
		return out, fmt.Errorf("validate fulfillment: %w", err)
	}

	sg := saga.New(ctx)
	// Reject WaitPaymentURL on terminal failure so the synchronous HTTP caller
	// doesn't hang when Run dies before the gateway loop ever resolves
	// payment_url_1. No-op if already resolved.
	sg.Defer("reject_payment_url", func(_ restate.Context) error {
		return restate.Promise[string](ctx, "payment_url_1").Reject(err)
	})
	defer func() {
		if restate.IsTerminalError(err) {
			sg.Compensate()
		}
	}()

	conf, err := h.confirm(ctx, sg, input, orderID)
	if err != nil {
		return out, err
	}

	outcome, err := h.escrow(ctx, sg, orderID, conf)
	if err != nil {
		return out, err
	}

	return FulfillmentOutput{OrderID: orderID, Outcome: outcome}, nil
}
