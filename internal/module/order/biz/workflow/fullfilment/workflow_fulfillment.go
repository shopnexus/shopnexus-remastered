package fullfilment

import (
	"fmt"
	"time"

	restate "github.com/restatedev/sdk-go"

	"shopnexus-server/internal/infras/locker"
	"shopnexus-server/internal/infras/metrics"
	accountbiz "shopnexus-server/internal/module/account/biz"
	accountmodel "shopnexus-server/internal/module/account/model"
	orderbase "shopnexus-server/internal/module/order/biz/base"
	"shopnexus-server/internal/module/order/biz/workflow/base"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/shared/saga"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
)

const (
	// escrowWindow is how long the seller payout sits in escrow before release.
	escrowWindow = 7 * 24 * time.Hour

	// mockTransportDeliveryDelay is how long the workflow waits before
	// pretending the return transport has been delivered. The buyer's window
	// to withdraw lasts until it fires.
	// TODO: remove when a real transport provider is wired up — its webhook
	// will resolve a delivered promise instead.
	mockTransportDeliveryDelay = 30 * time.Second
)

// RefundCrediter is the slice of the refund handler the escrow loop drives on
// auto-accept. Declared here (not imported from the refund package) so the
// dependency points one way: fulfillment → refund handler, never the reverse —
// the refund package already imports this package for FulfillmentWfClient.
// Exported so fx binds *refund.RefundHandler to it via fx.As.
type RefundCrediter interface {
	ExecuteRefundCredit(
		ctx restate.Context,
		refund orderdb.OrderRefund,
		deciderID uuid.UUID,
		reason ordermodel.RefundCreditReason,
	) (orderdb.OrderRefund, error)
}

// FulfillmentWorkflow drives one order's full lifecycle: seller confirm-fee
// saga → order creation → escrow watch → payout release or refund. The
// workflow key, the confirm session ID and the order ID are the same UUID,
// minted at HTTP submission time — the payment webhook, the FE retry/cancel
// endpoints and every refund/dispute signal all route by it.
type FulfillmentWorkflow struct {
	*orderbase.Base

	wf      *base.Base
	account accountbiz.AccountBizClient
	locker  locker.Client
	refund  RefundCrediter
}

func NewFulfillmentWorkflow(
	core *orderbase.Base,
	wf *base.Base,
	account accountbiz.AccountBizClient,
	locker locker.Client,
	refund RefundCrediter,
) *FulfillmentWorkflow {
	return &FulfillmentWorkflow{core, wf, account, locker, refund}
}

func (h *FulfillmentWorkflow) ServiceName() string { return "FulfillmentWorkflow" }

type FulfillmentInput struct {
	Account       accountmodel.AuthenticatedAccount `json:"account"`
	ItemIDs       []int64                           `json:"item_ids"            validate:"required,min=1,max=1000"`
	UseWallet     bool                              `json:"use_wallet"`
	WalletID      *uuid.UUID                        `json:"wallet_id,omitempty"`
	PaymentOption string                            `json:"payment_option"      validate:"max=100"`
	Note          string                            `json:"note"                validate:"max=500"`
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

func (h *FulfillmentWorkflow) Run(
	ctx restate.WorkflowContext,
	input FulfillmentInput,
) (out FulfillmentOutput, err error) {
	defer metrics.TrackHandler("fulfillment_workflow", "Run", &err)()

	orderID := uuid.MustParse(restate.Key(ctx))
	out.OrderID = orderID

	if err = validator.Validate(input); err != nil {
		return out, fmt.Errorf("validate fulfillment: %w", err)
	}

	sg := saga.New(ctx)
	// Reject WaitPaymentURL on terminal failure so the synchronous HTTP
	// caller doesn't hang when Run dies before the gateway loop ever
	// resolves payment_url_1. No-op if already resolved.
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

	out.Outcome, err = h.escrow(ctx, sg, orderID, conf)
	return out, err
}
