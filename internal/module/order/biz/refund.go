package orderbiz

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	restate "github.com/restatedev/sdk-go"

	"shopnexus-server/internal/infras/metrics"
	accountbiz "shopnexus-server/internal/module/account/biz"
	accountmodel "shopnexus-server/internal/module/account/model"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/validator"
)

// Refund v2 timers. Kept here so workflow + biz share one source of truth.
const (
	// sellerReviewWindow is how long the seller has from physical delivery to
	// decide accept/dispute before the refund auto-accepts in the buyer's favor.
	sellerReviewWindow = 3 * 24 * time.Hour
	// forwardTransportTimeout caps how long we wait for the return-transport
	// webhook to fire. After this, the refund auto-accepts (defends against
	// lost packages — the platform eats the loss rather than stranding the buyer).
	forwardTransportTimeout = 14 * 24 * time.Hour
)

// signalPayoutWorkflowOnRefundChanged fires a fire-and-forget signal to the
// PayoutWorkflow keyed on the refund's order ID, telling it to re-evaluate
// whether to release escrow or short-circuit into a refunded outcome.
func signalPayoutWorkflowOnRefundChanged(ctx restate.Context, orderID uuid.UUID) {
	restate.WorkflowSend(ctx, "PayoutWorkflow", orderID.String(), "OnRefundChanged").Send(struct{}{})
}

// notifyRefund pushes an in-app notification about a refund transition.
func notifyRefund(
	ctx restate.Context,
	accountID uuid.UUID,
	notiType accountmodel.NotificationType,
	title, content string,
	refund orderdb.OrderRefund,
) {
	meta, _ := json.Marshal(map[string]string{
		"refund_id": refund.ID.String(),
		"order_id":  refund.OrderID.String(),
	})
	restate.ServiceSend(ctx, "Account", "CreateNotification").Send(accountbiz.CreateNotificationParams{
		AccountID: accountID,
		Type:      notiType,
		Channel:   accountmodel.ChannelInApp,
		Title:     title,
		Content:   content,
		Metadata:  meta,
	})
}

// CreateBuyerRefund opens a refund. The buyer simultaneously commits to
// shipping the goods back: a return transport row is created on the spot and
// the refund starts in Shipping. Required: reason + photos + return option.
func (b *refundHandler) CreateBuyerRefund(
	ctx restate.Context,
	params CreateBuyerRefundParams,
) (ordermodel.Refund, error) {
	var zero ordermodel.Refund
	var err error
	defer metrics.TrackHandler("order", "CreateBuyerRefund", &err)()

	if err = validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate create refund: %w", err)
	}
	if len(params.Attachments) == 0 {
		return zero, ordermodel.ErrRefundEvidenceRequired
	}

	// Validate order ownership + paid state inside one durable Run so the
	// snapshot is journaled.
	type guardResult struct {
		Order  orderdb.OrderOrder `json:"order"`
		Item   orderdb.OrderItem  `json:"item"`
		Active bool               `json:"active"`
	}
	guard, err := restate.Run(ctx, func(rctx restate.RunContext) (guardResult, error) {
		order, e := b.storage.Querier().GetOrder(rctx, orderdb.GetOrderParams{
			ID: uuid.NullUUID{UUID: params.OrderID, Valid: true},
		})
		if e != nil {
			return guardResult{}, fmt.Errorf("get order: %w", e)
		}
		if order.BuyerID != params.Account.ID {
			return guardResult{}, ordermodel.ErrItemNotOwnedByBuyer
		}

		items, e := b.storage.Querier().ListItem(rctx, orderdb.ListItemParams{
			OrderID: []uuid.NullUUID{{UUID: params.OrderID, Valid: true}},
		})
		if e != nil {
			return guardResult{}, fmt.Errorf("list items: %w", e)
		}
		var anyItem orderdb.OrderItem
		for _, it := range items {
			if !it.DateCancelled.Valid {
				anyItem = it
				break
			}
		}
		if anyItem.ID == 0 {
			return guardResult{}, ordermodel.ErrItemAlreadyCancelled
		}

		// Order must have a settled positive tx in the buyer's session.
		txs, e := b.storage.Querier().ListTransactionsBySession(rctx, anyItem.PaymentSessionID)
		if e != nil {
			return guardResult{}, fmt.Errorf("list txs: %w", e)
		}
		if _, paid := findOriginalCharge(txs); !paid {
			return guardResult{}, ordermodel.ErrRefundOrderNotPaid
		}

		active, e := b.storage.Querier().HasActiveRefundForOrder(rctx, params.OrderID)
		if e != nil {
			return guardResult{}, fmt.Errorf("check active refund: %w", e)
		}
		return guardResult{Order: order, Item: anyItem, Active: active}, nil
	})
	if err != nil {
		return zero, err
	}
	if guard.Active {
		return zero, ordermodel.ErrRefundAlreadyAccepted
	}

	attachmentsJSON, err := json.Marshal(params.Attachments)
	if err != nil {
		return zero, fmt.Errorf("marshal attachments: %w", err)
	}

	// Create return transport + refund row in the same Run so a failure mid-way
	// doesn't leave an orphan transport. Transport starts in NULL/Pending — the
	// workflow's mock timer (or the real provider webhook) will mark it Success
	// later, giving the buyer a window to withdraw while it is still Shipping.
	refund, err := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderRefund, error) {
		returnTransport, e := b.storage.Querier().CreateDefaultTransport(rctx, orderdb.CreateDefaultTransportParams{
			Option: params.ReturnOption,
			Data:   json.RawMessage(`{"direction":"return","leg":"buyer-to-seller"}`),
		})
		if e != nil {
			return orderdb.OrderRefund{}, fmt.Errorf("create return transport: %w", e)
		}

		return b.storage.Querier().CreateBuyerRefund(rctx, orderdb.CreateBuyerRefundParams{
			AccountID:         params.Account.ID,
			OrderID:           params.OrderID,
			Reason:            params.Reason,
			Attachments:       attachmentsJSON,
			ReturnTransportID: returnTransport.ID,
		})
	})
	if err != nil {
		return zero, fmt.Errorf("create refund: %w", err)
	}

	// Spawn RefundWorkflow keyed on refund.ID to manage timers + signals.
	// The workflow handles the mock auto-deliver timer (currently 30s) so the
	// buyer has a window to withdraw before the seller is notified.
	restate.WorkflowSend(ctx, "RefundWorkflow", refund.ID.String(), "Run").Send(RefundWorkflowInput{
		RefundID: refund.ID,
		OrderID:  refund.OrderID,
		BuyerID:  refund.AccountID,
		SellerID: guard.Order.SellerID,
	})

	signalPayoutWorkflowOnRefundChanged(ctx, refund.OrderID)
	notifyRefund(ctx, guard.Order.SellerID, accountmodel.NotiRefundRequested,
		"New refund request", "A buyer has shipped items back and requested a refund.", refund)

	return mapRefund(refund), nil
}

// WithdrawBuyerRefund cancels a Shipping refund at the buyer's request. Only
// the buyer who created the refund can withdraw, and only while the goods are
// still in transit — once the seller has the items (AwaitingSellerReview),
// withdraw is blocked. The refund row flips to Cancelled (terminal), the
// payout watcher resumes the seller's escrow, and the workflow exits via the
// "withdrawn" promise.
func (b *refundHandler) WithdrawBuyerRefund(
	ctx restate.Context,
	params WithdrawBuyerRefundParams,
) (ordermodel.Refund, error) {
	var zero ordermodel.Refund
	var err error
	defer metrics.TrackHandler("order", "WithdrawBuyerRefund", &err)()

	if err = validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate withdraw refund: %w", err)
	}

	// SQL guards on status='Shipping' AND account_id=caller, so a row update of
	// zero means the refund is in a non-withdrawable state OR the caller is not
	// the buyer. We translate that to ErrRefundNotWithdrawable rather than
	// leaking ErrNoRows.
	refund, err := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderRefund, error) {
		return b.storage.Querier().WithdrawBuyerRefund(rctx, orderdb.WithdrawBuyerRefundParams{
			ID:        params.RefundID,
			AccountID: params.Account.ID,
		})
	})
	if err != nil {
		return zero, ordermodel.ErrRefundNotWithdrawable
	}

	// Tell the workflow to exit early; payout watcher resumes immediately.
	restate.WorkflowSend(ctx, "RefundWorkflow", refund.ID.String(), "OnBuyerWithdrew").Send(struct{}{})
	signalPayoutWorkflowOnRefundChanged(ctx, refund.OrderID)

	// Notify seller (was waiting on the inbound return) so their UI clears.
	order, err := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderOrder, error) {
		return b.storage.Querier().GetOrder(rctx, orderdb.GetOrderParams{
			ID: uuid.NullUUID{UUID: refund.OrderID, Valid: true},
		})
	})
	if err == nil {
		notifyRefund(ctx, order.SellerID, accountmodel.NotiRefundRequested,
			"Refund withdrawn", "The buyer withdrew their refund request before the return arrived.", refund)
	}

	return mapRefund(refund), nil
}

// SellerApproveRefund is the happy path: seller agrees with the refund after
// receiving the returned goods. Triggers the auto-credit flow.
func (b *refundHandler) SellerApproveRefund(
	ctx restate.Context,
	params SellerActionParams,
) (ordermodel.Refund, error) {
	var zero ordermodel.Refund
	var err error
	defer metrics.TrackHandler("order", "SellerApproveRefund", &err)()

	if err = validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate seller approve: %w", err)
	}

	refund, err := b.loadAndAuthSeller(ctx, params.RefundID, params.Account.ID)
	if err != nil {
		return zero, err
	}
	if !mapRefund(refund).CanSellerDecide() {
		return zero, ordermodel.ErrRefundWrongStage
	}

	updated, err := b.executeRefundCredit(ctx, refund, params.Account.ID, refundCreditReasonSellerApproved)
	if err != nil {
		return zero, err
	}

	restate.WorkflowSend(ctx, "RefundWorkflow", refund.ID.String(), "OnSellerDecision").Send(SellerDecisionSignal{Approved: true})

	notifyRefund(ctx, refund.AccountID, accountmodel.NotiRefundApproved,
		"Refund approved", "The seller approved your refund and your wallet has been credited.", updated)

	return mapRefund(updated), nil
}

// SellerDisputeRefund escalates the refund to admin. The seller provides a
// reason and evidence photos; the refund row flips to Disputed and a dispute
// row is opened.
func (b *refundHandler) SellerDisputeRefund(
	ctx restate.Context,
	params SellerDisputeParams,
) (ordermodel.RefundDispute, error) {
	var zero ordermodel.RefundDispute
	var err error
	defer metrics.TrackHandler("order", "SellerDisputeRefund", &err)()

	if err = validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate seller dispute: %w", err)
	}

	refund, err := b.loadAndAuthSeller(ctx, params.RefundID, params.Account.ID)
	if err != nil {
		return zero, err
	}
	if !mapRefund(refund).CanSellerDecide() {
		return zero, ordermodel.ErrRefundWrongStage
	}

	attachmentsJSON, err := json.Marshal(params.Attachments)
	if err != nil {
		return zero, fmt.Errorf("marshal attachments: %w", err)
	}

	dispute, err := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderRefundDispute, error) {
		if _, e := b.storage.Querier().SellerDisputeRefund(rctx, refund.ID); e != nil {
			return orderdb.OrderRefundDispute{}, fmt.Errorf("dispute refund: %w", e)
		}
		return b.storage.Querier().OpenRefundDispute(rctx, orderdb.OpenRefundDisputeParams{
			RefundID:    refund.ID,
			AccountID:   params.Account.ID,
			Reason:      params.Reason,
			Attachments: attachmentsJSON,
		})
	})
	if err != nil {
		return zero, fmt.Errorf("open dispute: %w", err)
	}

	restate.WorkflowSend(ctx, "RefundWorkflow", refund.ID.String(), "OnSellerDecision").Send(SellerDecisionSignal{Approved: false})

	notifyRefund(ctx, refund.AccountID, accountmodel.NotiDisputeOpened,
		"Refund disputed", "The seller has disputed your refund. Our team will review the case.", refund)

	return mapRefundDispute(dispute), nil
}

// loadAndAuthSeller fetches the refund and verifies the caller is the order's
// seller. Shared between SellerApproveRefund + SellerDisputeRefund.
func (b *refundHandler) loadAndAuthSeller(
	ctx restate.Context,
	refundID uuid.UUID,
	callerID uuid.UUID,
) (orderdb.OrderRefund, error) {
	refund, err := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderRefund, error) {
		return b.storage.Querier().GetRefund(rctx, orderdb.GetRefundParams{
			ID: uuid.NullUUID{UUID: refundID, Valid: true},
		})
	})
	if err != nil {
		return orderdb.OrderRefund{}, fmt.Errorf("get refund: %w", err)
	}
	order, err := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderOrder, error) {
		return b.storage.Querier().GetOrder(rctx, orderdb.GetOrderParams{
			ID: uuid.NullUUID{UUID: refund.OrderID, Valid: true},
		})
	})
	if err != nil {
		return orderdb.OrderRefund{}, fmt.Errorf("get order: %w", err)
	}
	if order.SellerID != callerID {
		return orderdb.OrderRefund{}, ordermodel.ErrItemNotOwnedBySeller
	}
	return refund, nil
}

// AutoAcceptRefund is called by RefundWorkflow when the 3-day review timer
// expires with no seller decision. Same credit flow as SellerApproveRefund,
// but no auth check (the caller is the workflow, not a user).
func (b *refundHandler) AutoAcceptRefund(
	ctx restate.Context,
	params AutoAcceptRefundParams,
) (ordermodel.Refund, error) {
	var zero ordermodel.Refund
	var err error
	defer metrics.TrackHandler("order", "AutoAcceptRefund", &err)()

	refund, err := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderRefund, error) {
		return b.storage.Querier().GetRefund(rctx, orderdb.GetRefundParams{
			ID: uuid.NullUUID{UUID: params.RefundID, Valid: true},
		})
	})
	if err != nil {
		return zero, fmt.Errorf("get refund: %w", err)
	}
	// Idempotent: another worker (or a manual seller decision) may have already
	// closed the refund. Treat that as success.
	if !mapRefund(refund).CanSellerDecide() {
		return mapRefund(refund), nil
	}

	updated, err := b.executeRefundCredit(ctx, refund, refund.AccountID, refundCreditReasonAutoAccepted)
	if err != nil {
		return zero, err
	}
	notifyRefund(ctx, refund.AccountID, accountmodel.NotiRefundApproved,
		"Refund auto-approved", "The seller did not respond in time, so your refund has been auto-approved and credited.", updated)
	return mapRefund(updated), nil
}

// MarkRefundDelivered flips a Shipping refund to AwaitingSellerReview when
// the return transport's webhook reports delivery. Called by RefundWorkflow
// after watching for the transport status change.
func (b *refundHandler) MarkRefundDelivered(
	ctx restate.Context,
	params MarkRefundDeliveredParams,
) (ordermodel.Refund, error) {
	var zero ordermodel.Refund
	var err error
	defer metrics.TrackHandler("order", "MarkRefundDelivered", &err)()

	deadline := time.Now().Add(sellerReviewWindow)
	updated, err := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderRefund, error) {
		return b.storage.Querier().MarkRefundDelivered(rctx, orderdb.MarkRefundDeliveredParams{
			ID:             params.RefundID,
			ReviewDeadline: null.TimeFrom(deadline),
		})
	})
	if err != nil {
		return zero, fmt.Errorf("mark delivered: %w", err)
	}

	// Get order to notify the seller their action is now expected.
	order, _ := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderOrder, error) {
		return b.storage.Querier().GetOrder(rctx, orderdb.GetOrderParams{
			ID: uuid.NullUUID{UUID: updated.OrderID, Valid: true},
		})
	})
	notifyRefund(ctx, order.SellerID, accountmodel.NotiRefundRequested,
		"Return delivered", "The buyer's return shipment has arrived. Please review within 3 days.", updated)

	return mapRefund(updated), nil
}

// ListBuyerRefunds returns paginated refunds owned by the requesting buyer.
func (b *refundHandler) ListBuyerRefunds(
	ctx restate.Context,
	params ListBuyerRefundsParams,
) (paginate.PaginateResult[ordermodel.Refund], error) {
	var zero paginate.PaginateResult[ordermodel.Refund]
	pagination := params.Params.Constrain()
	rows, err := b.storage.Querier().ListBuyerRefunds(ctx, orderdb.ListBuyerRefundsParams{
		AccountID:   params.BuyerID,
		OffsetCount: pagination.Offset().Int32,
		LimitCount:  pagination.Limit.Int32,
	})
	if err != nil {
		return zero, fmt.Errorf("list buyer refunds: %w", err)
	}
	data := make([]ordermodel.Refund, 0, len(rows))
	for _, r := range rows {
		data = append(data, mapRefund(r))
	}
	return paginate.PaginateResult[ordermodel.Refund]{PageParams: pagination, Data: data}, nil
}

// ListSellerRefunds returns refunds raised against orders the requesting seller fulfilled.
func (b *refundHandler) ListSellerRefunds(
	ctx restate.Context,
	params ListSellerRefundsParams,
) (paginate.PaginateResult[ordermodel.Refund], error) {
	var zero paginate.PaginateResult[ordermodel.Refund]
	pagination := params.Params.Constrain()
	rows, err := b.storage.Querier().ListSellerRefunds(ctx, orderdb.ListSellerRefundsParams{
		SellerID:    params.SellerID,
		OffsetCount: pagination.Offset().Int32,
		LimitCount:  pagination.Limit.Int32,
	})
	if err != nil {
		return zero, fmt.Errorf("list seller refunds: %w", err)
	}
	data := make([]ordermodel.Refund, 0, len(rows))
	for _, r := range rows {
		data = append(data, mapRefund(r))
	}
	return paginate.PaginateResult[ordermodel.Refund]{PageParams: pagination, Data: data}, nil
}

type ListBuyerRefundsParams struct {
	paginate.Params

	BuyerID uuid.UUID `validate:"required"`
}

type ListSellerRefundsParams struct {
	paginate.Params

	SellerID uuid.UUID `validate:"required"`
}

type CreateBuyerRefundParams struct {
	Account      accountmodel.AuthenticatedAccount
	OrderID      uuid.UUID           `json:"order_id" validate:"required"`
	Reason       string              `json:"reason" validate:"required,min=1,max=1000"`
	Attachments  []DisputeAttachment `json:"attachments" validate:"required,min=1,max=20,dive"`
	ReturnOption string              `json:"return_option" validate:"required,min=1,max=100"`
}

// SellerActionParams covers SellerApproveRefund (no body needed).
type SellerActionParams struct {
	Account  accountmodel.AuthenticatedAccount
	RefundID uuid.UUID `json:"refund_id" validate:"required"`
}

// WithdrawBuyerRefundParams covers the buyer-initiated cancel. Caller must be
// the refund's buyer and the refund must be in Shipping.
type WithdrawBuyerRefundParams struct {
	Account  accountmodel.AuthenticatedAccount
	RefundID uuid.UUID `json:"refund_id" validate:"required"`
}

type SellerDisputeParams struct {
	Account     accountmodel.AuthenticatedAccount
	RefundID    uuid.UUID           `json:"refund_id"   validate:"required"`
	Reason      string              `json:"reason"      validate:"required,min=1,max=1000"`
	Attachments []DisputeAttachment `json:"attachments" validate:"required,min=1,max=20,dive"`
}

type MarkRefundDeliveredParams struct {
	RefundID uuid.UUID `json:"refund_id" validate:"required"`
}

type AutoAcceptRefundParams struct {
	RefundID uuid.UUID `json:"refund_id" validate:"required"`
}

// DisputeAttachment is a single piece of evidence (image URL + meta). Used by
// the refund-create and seller-dispute flows.
type DisputeAttachment struct {
	URL  string `json:"url"  validate:"required,url,max=1000"`
	Kind string `json:"kind" validate:"omitempty,max=50"`
	Name string `json:"name" validate:"omitempty,max=255"`
}
