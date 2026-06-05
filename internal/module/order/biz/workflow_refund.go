package orderbiz

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	restate "github.com/restatedev/sdk-go"

	"shopnexus-server/internal/infras/metrics"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
)

// mockTransportDeliveryDelay is how long the workflow waits before pretending
// the return transport has been delivered. Real provider webhooks will fire
// the same promise earlier (or later) than this timer; the buyer's window to
// withdraw lasts until either event happens.
//
// TODO: remove this constant when a real transport provider is wired up. The
// production path will rely on the carrier's webhook resolving the
// `delivered` promise, with no synthetic timer needed.
const mockTransportDeliveryDelay = 30 * time.Second

// RefundWorkflow drives the v2 refund lifecycle from the moment the buyer
// ships the goods back. One workflow instance per refund, keyed by refund ID.
//
//  1. Wait for {return transport delivered | buyer withdraw | 14D shipping
//     timeout | 30s mock-deliver timer}.
//  2. Once delivered, wait for {seller approve / dispute | 3D auto-accept}.
//  3. If disputed, wait for {admin uphold / dismiss}.
type RefundWorkflow struct {
	base *OrderHandler
}

func NewRefundWorkflow(base *OrderHandler) *RefundWorkflow {
	return &RefundWorkflow{base: base}
}

func (h *RefundWorkflow) ServiceName() string { return "RefundWorkflow" }

// RefundWorkflowInput is the payload Run is invoked with on refund creation.
type RefundWorkflowInput struct {
	RefundID uuid.UUID `json:"refund_id"`
	OrderID  uuid.UUID `json:"order_id"`
	BuyerID  uuid.UUID `json:"buyer_id"`
	SellerID uuid.UUID `json:"seller_id"`
}

// RefundWorkflowOutput is the final outcome the workflow records before exit.
type RefundWorkflowOutput struct {
	RefundID uuid.UUID `json:"refund_id"`
	Outcome  string    `json:"outcome"` // "accepted" | "rejected" | "expired-shipping" | "withdrawn"
}

// SellerDecisionSignal is sent by SellerApproveRefund / SellerDisputeRefund.
type SellerDecisionSignal struct {
	Approved bool `json:"approved"`
}

// AdminDecisionSignal is sent by AdminUpholdDispute / AdminDismissDispute.
type AdminDecisionSignal struct {
	Upheld bool `json:"upheld"`
}

func (h *RefundWorkflow) Run(
	ctx restate.WorkflowContext,
	input RefundWorkflowInput,
) (out RefundWorkflowOutput, err error) {
	defer metrics.TrackHandler("refund_workflow", "Run", &err)()
	out.RefundID = input.RefundID

	logger := slog.With("refund_id", input.RefundID, "order_id", input.OrderID)
	logger.Info("RefundWorkflow.Run started")

	// Phase 1: race four futures —
	//   - delivered (real webhook resolves this)
	//   - withdrawn (buyer cancels)
	//   - mock-deliver timer (dev-only synthetic delivery)
	//   - 14D shipping timeout (carrier lost the package)
	deliveredPromise := restate.Promise[any](ctx, "delivered")
	withdrawnPromise := restate.Promise[any](ctx, "withdrawn")
	mockDeliverTimer := restate.After(ctx, mockTransportDeliveryDelay)
	forwardDeadline := restate.After(ctx, forwardTransportTimeout)

	winner, err := restate.WaitFirst(ctx, deliveredPromise, withdrawnPromise, mockDeliverTimer, forwardDeadline)
	if err != nil {
		return out, fmt.Errorf("wait phase 1: %w", err)
	}

	switch winner {
	case withdrawnPromise:
		// Buyer already updated the refund row to Cancelled via biz handler.
		// Just record the outcome and exit; payout watcher has already been
		// signalled to resume escrow.
		logger.Info("refund withdrawn by buyer")
		out.Outcome = "withdrawn"
		return out, nil

	case forwardDeadline:
		// Carrier lost the package — platform eats the loss, buyer gets credit.
		logger.Warn("forward transport timed out, auto-accepting refund")
		if _, err = h.base.AutoAcceptRefund(ctx, AutoAcceptRefundParams{RefundID: input.RefundID}); err != nil {
			return out, fmt.Errorf("auto-accept on shipping timeout: %w", err)
		}
		out.Outcome = "expired-shipping"
		return out, nil

	case mockDeliverTimer:
		// Mock-only: flip the transport row to Success so subsequent UI loads
		// see it as delivered. Then fall through to the delivered branch.
		// TODO: remove this branch when a real provider is wired up.
		if err = h.markMockTransportDelivered(ctx, input.RefundID); err != nil {
			return out, err
		}
		// fall through to delivered handling

	case deliveredPromise:
		// Real provider webhook resolved the promise; transport row already
		// updated by OnTransportResult webhook handler.
	}

	// Goods delivered → flip refund row + arm 3D seller review timer.
	if _, err = h.base.MarkRefundDelivered(ctx, MarkRefundDeliveredParams{RefundID: input.RefundID}); err != nil {
		return out, fmt.Errorf("mark delivered: %w", err)
	}

	// Phase 2: wait for seller decision OR 3D auto-accept.
	sellerDecisionPromise := restate.Promise[SellerDecisionSignal](ctx, "seller_decision")
	reviewDeadline := restate.After(ctx, sellerReviewWindow)
	winner2, err := restate.WaitFirst(ctx, sellerDecisionPromise, reviewDeadline)
	if err != nil {
		return out, fmt.Errorf("wait seller decision: %w", err)
	}

	if winner2 == reviewDeadline {
		logger.Info("seller review window elapsed, auto-accepting")
		if _, err = h.base.AutoAcceptRefund(ctx, AutoAcceptRefundParams{RefundID: input.RefundID}); err != nil {
			return out, fmt.Errorf("auto-accept on review timeout: %w", err)
		}
		out.Outcome = "accepted"
		return out, nil
	}

	sellerDecision, err := sellerDecisionPromise.Result()
	if err != nil {
		return out, fmt.Errorf("read seller decision: %w", err)
	}
	if sellerDecision.Approved {
		out.Outcome = "accepted"
		return out, nil
	}

	// Phase 3: seller disputed → wait for admin verdict. No SLA timer yet;
	// admin must resolve manually. Could add a default-buyer-wins timer later.
	adminPromise := restate.Promise[AdminDecisionSignal](ctx, "admin_decision")
	adminDecision, err := adminPromise.Result()
	if err != nil {
		return out, fmt.Errorf("wait admin decision: %w", err)
	}
	if adminDecision.Upheld {
		out.Outcome = "rejected"
	} else {
		out.Outcome = "accepted"
	}
	return out, nil
}

// markMockTransportDelivered updates the return transport row to Success.
// Called from the mock-deliver timer branch so subsequent reads of the
// transport row (FE, GET /buyer/orders/:id, etc.) show the delivery.
// TODO: remove when real transport provider is wired up.
func (h *RefundWorkflow) markMockTransportDelivered(
	ctx restate.WorkflowContext,
	refundID uuid.UUID,
) error {
	refund, err := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderRefund, error) {
		return h.base.storage.Querier().GetRefund(rctx, orderdb.GetRefundParams{
			ID: uuid.NullUUID{UUID: refundID, Valid: true},
		})
	})
	if err != nil {
		return fmt.Errorf("mock load refund: %w", err)
	}
	if _, err = restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderTransport, error) {
		return h.base.storage.Querier().UpdateTransportStatusByID(rctx, orderdb.UpdateTransportStatusByIDParams{
			ID:     refund.ReturnTransportID,
			Status: orderdb.NullOrderStatus{OrderStatus: orderdb.OrderStatusSuccess, Valid: true},
			Data:   json.RawMessage(`{"direction":"return","leg":"buyer-to-seller","mock":"auto-delivered"}`),
		})
	}); err != nil {
		return fmt.Errorf("mock mark transport success: %w", err)
	}
	_ = null.String{} // keep guregu/null/v6 import in case future fields need it
	return nil
}

// OnTransportDelivered is signalled by the transport webhook when the return
// shipment reaches its final state. Resolves the "delivered" promise.
func (h *RefundWorkflow) OnTransportDelivered(
	ctx restate.WorkflowSharedContext,
	_ struct{},
) error {
	return restate.Promise[any](ctx, "delivered").Resolve(nil)
}

// OnBuyerWithdrew is signalled by WithdrawBuyerRefund to abort Phase 1.
func (h *RefundWorkflow) OnBuyerWithdrew(
	ctx restate.WorkflowSharedContext,
	_ struct{},
) error {
	return restate.Promise[any](ctx, "withdrawn").Resolve(nil)
}

// OnSellerDecision is signalled by SellerApproveRefund and SellerDisputeRefund.
func (h *RefundWorkflow) OnSellerDecision(
	ctx restate.WorkflowSharedContext,
	signal SellerDecisionSignal,
) error {
	return restate.Promise[SellerDecisionSignal](ctx, "seller_decision").Resolve(signal)
}

// OnAdminDecision is signalled by AdminUpholdDispute and AdminDismissDispute.
func (h *RefundWorkflow) OnAdminDecision(
	ctx restate.WorkflowSharedContext,
	signal AdminDecisionSignal,
) error {
	return restate.Promise[AdminDecisionSignal](ctx, "admin_decision").Resolve(signal)
}

// SignalRefundDeliveredFromTransport is a helper for OnTransportResult to fire
// the workflow's delivered promise once we recognise the transport ID belongs
// to a refund. Kept as a free function (not a method on RefundWorkflow) so the
// caller doesn't need a reference.
func SignalRefundDeliveredFromTransport(ctx restate.Context, refundID uuid.UUID) {
	restate.WorkflowSend(ctx, "RefundWorkflow", refundID.String(), "OnTransportDelivered").Send(struct{}{})
}
