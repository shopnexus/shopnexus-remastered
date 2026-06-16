package orderrepo

import (
	"context"

	"github.com/google/uuid"
	null "github.com/guregu/null/v6"
	ordermodel "shopnexus-server/internal/module/order/model"
)

const createBuyerRefund = `-- name: CreateBuyerRefund :one

INSERT INTO "order"."refund" (
    "account_id", "order_id", "reason", "return_transport_id"
) VALUES (
    $1, $2, $3, $4
)
RETURNING id, account_id, order_id, reason, date_created, status, return_transport_id, date_received_by_seller, review_deadline, seller_decision_at, return_to_buyer_transport_id, rejection_reason, refund_tx_id
`

type CreateBuyerRefundParams struct {
	AccountID         uuid.UUID `json:"account_id"`
	OrderID           uuid.UUID `json:"order_id"`
	Reason            string    `json:"reason"`
	ReturnTransportID int64     `json:"return_transport_id"`
}

// CreateBuyerRefund inserts the initial Shipping row. Evidence photos attach
// separately via the common resource system (RefType=Refund); the return-leg
// transport ID is already provisioned by the biz layer.
func (r *Repository) CreateBuyerRefund(ctx context.Context, arg CreateBuyerRefundParams) (ordermodel.Refund, error) {
	row := r.db.QueryRow(ctx, createBuyerRefund,
		arg.AccountID,
		arg.OrderID,
		arg.Reason,
		arg.ReturnTransportID,
	)
	var i ordermodel.Refund
	err := row.Scan(
		&i.ID,
		&i.AccountID,
		&i.OrderID,
		&i.Reason,
		&i.DateCreated,
		&i.Status,
		&i.ReturnTransportID,
		&i.DateReceivedBySeller,
		&i.ReviewDeadline,
		&i.SellerDecisionAt,
		&i.ReturnToBuyerTransportID,
		&i.RejectionReason,
		&i.RefundTxID,
	)
	return i, err
}

const withdrawBuyerRefund = `-- name: WithdrawBuyerRefund :one
UPDATE "order"."refund" AS r
SET "status" = 'Cancelled'
FROM "order"."transport" AS t
WHERE r."id" = $1
  AND r."account_id" = $2
  AND r."status" = 'Shipping'
  AND t."id" = r."return_transport_id"
  AND t."status" = 'Pending'
RETURNING r.id, r.account_id, r.order_id, r.reason, r.date_created, r.status, r.return_transport_id, r.date_received_by_seller, r.review_deadline, r.seller_decision_at, r.return_to_buyer_transport_id, r.rejection_reason, r.refund_tx_id
`

type WithdrawBuyerRefundParams struct {
	ID        uuid.UUID `json:"id"`
	AccountID uuid.UUID `json:"account_id"`
}

// WithdrawBuyerRefund cancels a refund only while the return transport is still
// Pending (not yet picked up by the carrier). Once the carrier starts moving it
// (Processing onward), withdraw is blocked — the goods have left the buyer. The
// join on return_transport_id is the authoritative gate; refund.status='Shipping'
// alone spans Pending+Processing, so it is not sufficient on its own.
func (r *Repository) WithdrawBuyerRefund(ctx context.Context, arg WithdrawBuyerRefundParams) (ordermodel.Refund, error) {
	row := r.db.QueryRow(ctx, withdrawBuyerRefund, arg.ID, arg.AccountID)
	var i ordermodel.Refund
	err := row.Scan(
		&i.ID,
		&i.AccountID,
		&i.OrderID,
		&i.Reason,
		&i.DateCreated,
		&i.Status,
		&i.ReturnTransportID,
		&i.DateReceivedBySeller,
		&i.ReviewDeadline,
		&i.SellerDecisionAt,
		&i.ReturnToBuyerTransportID,
		&i.RejectionReason,
		&i.RefundTxID,
	)
	return i, err
}

const markRefundDelivered = `-- name: MarkRefundDelivered :one
UPDATE "order"."refund"
SET "status" = 'AwaitingSellerReview',
    "date_received_by_seller" = CURRENT_TIMESTAMP,
    "review_deadline" = $1
WHERE "id" = $2 AND "status" = 'Shipping'
RETURNING id, account_id, order_id, reason, date_created, status, return_transport_id, date_received_by_seller, review_deadline, seller_decision_at, return_to_buyer_transport_id, rejection_reason, refund_tx_id
`

type MarkRefundDeliveredParams struct {
	ReviewDeadline null.Time `json:"review_deadline"`
	ID             uuid.UUID `json:"id"`
}

// MarkRefundDelivered fires when the forward (return) transport reaches its
// final delivered state, flipping the refund into AwaitingSellerReview and
// arming the 3-day auto-accept deadline.
func (r *Repository) MarkRefundDelivered(ctx context.Context, arg MarkRefundDeliveredParams) (ordermodel.Refund, error) {
	row := r.db.QueryRow(ctx, markRefundDelivered, arg.ReviewDeadline, arg.ID)
	var i ordermodel.Refund
	err := row.Scan(
		&i.ID,
		&i.AccountID,
		&i.OrderID,
		&i.Reason,
		&i.DateCreated,
		&i.Status,
		&i.ReturnTransportID,
		&i.DateReceivedBySeller,
		&i.ReviewDeadline,
		&i.SellerDecisionAt,
		&i.ReturnToBuyerTransportID,
		&i.RejectionReason,
		&i.RefundTxID,
	)
	return i, err
}

const sellerApproveRefund = `-- name: SellerApproveRefund :one
UPDATE "order"."refund"
SET "status" = 'Accepted',
    "seller_decision_at" = CURRENT_TIMESTAMP,
    "refund_tx_id" = $1
WHERE "id" = $2 AND "status" = 'AwaitingSellerReview'
RETURNING id, account_id, order_id, reason, date_created, status, return_transport_id, date_received_by_seller, review_deadline, seller_decision_at, return_to_buyer_transport_id, rejection_reason, refund_tx_id
`

type SellerApproveRefundParams struct {
	RefundTxID uuid.NullUUID `json:"refund_tx_id"`
	ID         uuid.UUID     `json:"id"`
}

// SellerApproveRefund transitions AwaitingSellerReview → Accepted and stamps
// the refund_tx_id pointing at the negative ledger leg the biz layer just
// inserted. Same SQL is used for auto-accept on deadline (caller passes the
// system's UUID or NULL for the implicit decider).
func (r *Repository) SellerApproveRefund(ctx context.Context, arg SellerApproveRefundParams) (ordermodel.Refund, error) {
	row := r.db.QueryRow(ctx, sellerApproveRefund, arg.RefundTxID, arg.ID)
	var i ordermodel.Refund
	err := row.Scan(
		&i.ID,
		&i.AccountID,
		&i.OrderID,
		&i.Reason,
		&i.DateCreated,
		&i.Status,
		&i.ReturnTransportID,
		&i.DateReceivedBySeller,
		&i.ReviewDeadline,
		&i.SellerDecisionAt,
		&i.ReturnToBuyerTransportID,
		&i.RejectionReason,
		&i.RefundTxID,
	)
	return i, err
}

const sellerDisputeRefund = `-- name: SellerDisputeRefund :one
UPDATE "order"."refund"
SET "status" = 'Disputed',
    "seller_decision_at" = CURRENT_TIMESTAMP
WHERE "id" = $1 AND "status" = 'AwaitingSellerReview'
RETURNING id, account_id, order_id, reason, date_created, status, return_transport_id, date_received_by_seller, review_deadline, seller_decision_at, return_to_buyer_transport_id, rejection_reason, refund_tx_id
`

// SellerDisputeRefund transitions AwaitingSellerReview → Disputed when the
// seller refuses the refund. A companion row is inserted into refund_dispute
// in the same biz call.
func (r *Repository) SellerDisputeRefund(ctx context.Context, id uuid.UUID) (ordermodel.Refund, error) {
	row := r.db.QueryRow(ctx, sellerDisputeRefund, id)
	var i ordermodel.Refund
	err := row.Scan(
		&i.ID,
		&i.AccountID,
		&i.OrderID,
		&i.Reason,
		&i.DateCreated,
		&i.Status,
		&i.ReturnTransportID,
		&i.DateReceivedBySeller,
		&i.ReviewDeadline,
		&i.SellerDecisionAt,
		&i.ReturnToBuyerTransportID,
		&i.RejectionReason,
		&i.RefundTxID,
	)
	return i, err
}

const adminDismissDispute = `-- name: AdminDismissDispute :one
UPDATE "order"."refund"
SET "status" = 'Accepted',
    "refund_tx_id" = $1
WHERE "id" = $2 AND "status" = 'Disputed'
RETURNING id, account_id, order_id, reason, date_created, status, return_transport_id, date_received_by_seller, review_deadline, seller_decision_at, return_to_buyer_transport_id, rejection_reason, refund_tx_id
`

type AdminDismissDisputeParams struct {
	RefundTxID uuid.NullUUID `json:"refund_tx_id"`
	ID         uuid.UUID     `json:"id"`
}

// AdminDismissDispute: admin sides with the buyer → refund flips
// Disputed → Accepted and the credit ledger leg is recorded.
func (r *Repository) AdminDismissDispute(ctx context.Context, arg AdminDismissDisputeParams) (ordermodel.Refund, error) {
	row := r.db.QueryRow(ctx, adminDismissDispute, arg.RefundTxID, arg.ID)
	var i ordermodel.Refund
	err := row.Scan(
		&i.ID,
		&i.AccountID,
		&i.OrderID,
		&i.Reason,
		&i.DateCreated,
		&i.Status,
		&i.ReturnTransportID,
		&i.DateReceivedBySeller,
		&i.ReviewDeadline,
		&i.SellerDecisionAt,
		&i.ReturnToBuyerTransportID,
		&i.RejectionReason,
		&i.RefundTxID,
	)
	return i, err
}

const adminUpholdDispute = `-- name: AdminUpholdDispute :one
UPDATE "order"."refund"
SET "status" = 'Rejected',
    "return_to_buyer_transport_id" = $1,
    "rejection_reason" = $2
WHERE "id" = $3 AND "status" = 'Disputed'
RETURNING id, account_id, order_id, reason, date_created, status, return_transport_id, date_received_by_seller, review_deadline, seller_decision_at, return_to_buyer_transport_id, rejection_reason, refund_tx_id
`

type AdminUpholdDisputeParams struct {
	ReturnToBuyerTransportID null.Int    `json:"return_to_buyer_transport_id"`
	RejectionReason          null.String `json:"rejection_reason"`
	ID                       uuid.UUID   `json:"id"`
}

// AdminUpholdDispute: admin sides with the seller → refund flips
// Disputed → Rejected and the return-to-buyer transport is recorded so the
// workflow can track delivery of the goods back.
func (r *Repository) AdminUpholdDispute(ctx context.Context, arg AdminUpholdDisputeParams) (ordermodel.Refund, error) {
	row := r.db.QueryRow(ctx, adminUpholdDispute, arg.ReturnToBuyerTransportID, arg.RejectionReason, arg.ID)
	var i ordermodel.Refund
	err := row.Scan(
		&i.ID,
		&i.AccountID,
		&i.OrderID,
		&i.Reason,
		&i.DateCreated,
		&i.Status,
		&i.ReturnTransportID,
		&i.DateReceivedBySeller,
		&i.ReviewDeadline,
		&i.SellerDecisionAt,
		&i.ReturnToBuyerTransportID,
		&i.RejectionReason,
		&i.RefundTxID,
	)
	return i, err
}

const getRefundByReturnTransportID = `SELECT id, account_id, order_id, reason, date_created, status, return_transport_id, date_received_by_seller, review_deadline, seller_decision_at, return_to_buyer_transport_id, rejection_reason, refund_tx_id
FROM "order"."refund"
WHERE "return_transport_id" = $1
LIMIT 1`

func (r *Repository) GetRefundByReturnTransportID(ctx context.Context, returnTransportID int64) (ordermodel.Refund, error) {
	row := r.db.QueryRow(ctx, getRefundByReturnTransportID, returnTransportID)
	var i ordermodel.Refund
	err := row.Scan(
		&i.ID,
		&i.AccountID,
		&i.OrderID,
		&i.Reason,
		&i.DateCreated,
		&i.Status,
		&i.ReturnTransportID,
		&i.DateReceivedBySeller,
		&i.ReviewDeadline,
		&i.SellerDecisionAt,
		&i.ReturnToBuyerTransportID,
		&i.RejectionReason,
		&i.RefundTxID,
	)
	return i, err
}

const hasActiveRefundForOrder = `-- name: HasActiveRefundForOrder :one
SELECT EXISTS (
    SELECT 1 FROM "order"."refund"
    WHERE "order_id" = $1
      AND "status" IN ('Shipping', 'AwaitingSellerReview', 'Disputed')
) AS has_active
`

// HasActiveRefundForOrder reports whether any refund row for this order is
// still in negotiation (not yet Accepted or Rejected). Used by the fulfillment
// workflow to decide whether to release escrow.
func (r *Repository) HasActiveRefundForOrder(ctx context.Context, orderID uuid.UUID) (bool, error) {
	row := r.db.QueryRow(ctx, hasActiveRefundForOrder, orderID)
	var hasActive bool
	err := row.Scan(&hasActive)
	return hasActive, err
}

const getRefundSnapshotByOrder = `-- name: GetRefundSnapshotByOrder :one
WITH all_refunds AS (
    SELECT "id", "status", "date_created"
    FROM "order"."refund"
    WHERE "order_id" = $1
)
SELECT
    EXISTS (
        SELECT 1 FROM all_refunds
        WHERE "status" IN ('Shipping', 'AwaitingSellerReview', 'Disputed')
    )::BOOLEAN AS has_active_refund,
    COALESCE(
        (SELECT "status" FROM all_refunds ORDER BY "date_created" DESC LIMIT 1) = 'Accepted'::"order"."refund_status",
        false
    )::BOOLEAN AS last_refund_approved,
    COALESCE(
        (
            SELECT "id" FROM all_refunds
            WHERE "status" IN ('Shipping', 'AwaitingSellerReview', 'Disputed')
            ORDER BY "date_created" DESC LIMIT 1
        ),
        '00000000-0000-0000-0000-000000000000'::uuid
    )::uuid AS active_refund_id
`

// GetRefundSnapshotByOrder is the per-iteration projection the fulfillment
// workflow reads while watching escrow.
func (r *Repository) GetRefundSnapshotByOrder(ctx context.Context, orderID uuid.UUID) (ordermodel.RefundSnapshot, error) {
	row := r.db.QueryRow(ctx, getRefundSnapshotByOrder, orderID)
	var i ordermodel.RefundSnapshot
	err := row.Scan(&i.HasActiveRefund, &i.LastRefundApproved, &i.ActiveRefundID)
	return i, err
}

const listSellerRefunds = `-- name: ListSellerRefunds :many
SELECT embed_refund.id, embed_refund.account_id, embed_refund.order_id, embed_refund.reason, embed_refund.date_created, embed_refund.status, embed_refund.return_transport_id, embed_refund.date_received_by_seller, embed_refund.review_deadline, embed_refund.seller_decision_at, embed_refund.return_to_buyer_transport_id, embed_refund.rejection_reason, embed_refund.refund_tx_id, COUNT(*) OVER() AS total_count
FROM "order"."refund" embed_refund
JOIN "order"."order" o ON o."id" = embed_refund."order_id"
WHERE o."seller_id" = $1
ORDER BY embed_refund."date_created" DESC
LIMIT $3::INTEGER OFFSET $2::INTEGER
`

type ListSellerRefundsParams struct {
	SellerID uuid.UUID  `json:"seller_id"`
	Offset   null.Int32 `json:"offset"`
	Limit    null.Int32 `json:"limit"`
}

// ListSellerRefunds returns refunds raised against orders the seller fulfilled,
// newest first, with COUNT(*) OVER() for page-based pagination. Buyer-side
// listing uses the generated ListRefund (account_id filter, no join).
func (r *Repository) ListSellerRefunds(ctx context.Context, arg ListSellerRefundsParams) ([]ordermodel.WithTotal[ordermodel.Refund], error) {
	rows, err := r.db.Query(ctx, listSellerRefunds, arg.SellerID, arg.Offset, arg.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ordermodel.WithTotal[ordermodel.Refund]
	for rows.Next() {
		var w ordermodel.WithTotal[ordermodel.Refund]
		if err := rows.Scan(
			&w.Row.ID,
			&w.Row.AccountID,
			&w.Row.OrderID,
			&w.Row.Reason,
			&w.Row.DateCreated,
			&w.Row.Status,
			&w.Row.ReturnTransportID,
			&w.Row.DateReceivedBySeller,
			&w.Row.ReviewDeadline,
			&w.Row.SellerDecisionAt,
			&w.Row.ReturnToBuyerTransportID,
			&w.Row.RejectionReason,
			&w.Row.RefundTxID,
			&w.TotalCount,
		); err != nil {
			return nil, err
		}
		items = append(items, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
