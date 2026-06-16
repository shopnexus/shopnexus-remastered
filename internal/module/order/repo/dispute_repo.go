package orderrepo

import (
	"context"

	"github.com/google/uuid"
	null "github.com/guregu/null/v6"
	ordermodel "shopnexus-server/internal/module/order/model"
)

const openRefundDispute = `-- name: OpenRefundDispute :one

INSERT INTO "order"."refund_dispute" (
    "refund_id", "account_id", "reason"
) VALUES (
    $1, $2, $3
)
RETURNING id, refund_id, account_id, reason, date_created, status, resolved_by_id, date_resolved, resolution_note
`

type OpenRefundDisputeParams struct {
	RefundID  uuid.UUID `json:"refund_id"`
	AccountID uuid.UUID `json:"account_id"`
	Reason    string    `json:"reason"`
}

// OpenRefundDispute inserts the seller's escalation. Status defaults to 'Open'
// via the table default; admin will transition to SellerWins or BuyerWins via
// ResolveRefundDispute.
func (r *Repository) OpenRefundDispute(ctx context.Context, arg OpenRefundDisputeParams) (ordermodel.RefundDispute, error) {
	row := r.db.QueryRow(ctx, openRefundDispute, arg.RefundID, arg.AccountID, arg.Reason)
	var i ordermodel.RefundDispute
	err := row.Scan(
		&i.ID,
		&i.RefundID,
		&i.AccountID,
		&i.Reason,
		&i.DateCreated,
		&i.Status,
		&i.ResolvedByID,
		&i.DateResolved,
		&i.ResolutionNote,
	)
	return i, err
}

const resolveRefundDispute = `-- name: ResolveRefundDispute :one
UPDATE "order"."refund_dispute"
SET "status" = $1,
    "resolved_by_id" = $2,
    "resolution_note" = $3,
    "date_resolved" = CURRENT_TIMESTAMP
WHERE "id" = $4 AND "status" = 'Open'
RETURNING id, refund_id, account_id, reason, date_created, status, resolved_by_id, date_resolved, resolution_note
`

type ResolveRefundDisputeParams struct {
	Status         ordermodel.DisputeStatus `json:"status"`
	ResolvedByID   uuid.NullUUID            `json:"resolved_by_id"`
	ResolutionNote null.String              `json:"resolution_note"`
	ID             uuid.UUID                `json:"id"`
}

// ResolveRefundDispute closes an Open dispute with the admin's verdict.
// Status must be either SellerWins or BuyerWins; the companion refund row
// update happens in the same biz transaction via AdminUphold/AdminDismiss.
func (r *Repository) ResolveRefundDispute(ctx context.Context, arg ResolveRefundDisputeParams) (ordermodel.RefundDispute, error) {
	row := r.db.QueryRow(ctx, resolveRefundDispute,
		arg.Status,
		arg.ResolvedByID,
		arg.ResolutionNote,
		arg.ID,
	)
	var i ordermodel.RefundDispute
	err := row.Scan(
		&i.ID,
		&i.RefundID,
		&i.AccountID,
		&i.Reason,
		&i.DateCreated,
		&i.Status,
		&i.ResolvedByID,
		&i.DateResolved,
		&i.ResolutionNote,
	)
	return i, err
}

const listRefundDisputes = `-- name: ListRefundDisputes :many
SELECT embed_refund_dispute.id, embed_refund_dispute.refund_id, embed_refund_dispute.account_id, embed_refund_dispute.reason, embed_refund_dispute.date_created, embed_refund_dispute.status, embed_refund_dispute.resolved_by_id, embed_refund_dispute.date_resolved, embed_refund_dispute.resolution_note, COUNT(*) OVER() AS total_count
FROM "order"."refund_dispute" embed_refund_dispute
JOIN "order"."refund" r ON r."id" = embed_refund_dispute."refund_id"
JOIN "order"."order" o ON o."id" = r."order_id"
WHERE ($1::"order"."dispute_status" IS NULL OR embed_refund_dispute."status" = $1)
  AND ($2::UUID IS NULL OR embed_refund_dispute."refund_id" = $2)
  AND (
       $3::UUID IS NULL
    OR r."account_id" = $3
    OR o."seller_id"   = $4
  )
ORDER BY embed_refund_dispute."date_created" DESC
LIMIT $6::INTEGER OFFSET $5::INTEGER
`

type ListRefundDisputesParams struct {
	Status         null.String   `json:"status"`
	RefundID       uuid.NullUUID `json:"refund_id"`
	CallerBuyerID  uuid.NullUUID `json:"caller_buyer_id"`
	CallerSellerID uuid.NullUUID `json:"caller_seller_id"`
	Offset         null.Int32    `json:"offset"`
	Limit          null.Int32    `json:"limit"`
}

// ListRefundDisputes powers the admin queue, with COUNT(*) OVER() for
// page-based pagination. Filters by status; admins see everything,
// buyers/sellers see their own.
func (r *Repository) ListRefundDisputes(ctx context.Context, arg ListRefundDisputesParams) ([]ordermodel.WithTotal[ordermodel.RefundDispute], error) {
	rows, err := r.db.Query(ctx, listRefundDisputes,
		arg.Status,
		arg.RefundID,
		arg.CallerBuyerID,
		arg.CallerSellerID,
		arg.Offset,
		arg.Limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ordermodel.WithTotal[ordermodel.RefundDispute]
	for rows.Next() {
		var w ordermodel.WithTotal[ordermodel.RefundDispute]
		if err := rows.Scan(
			&w.Row.ID,
			&w.Row.RefundID,
			&w.Row.AccountID,
			&w.Row.Reason,
			&w.Row.DateCreated,
			&w.Row.Status,
			&w.Row.ResolvedByID,
			&w.Row.DateResolved,
			&w.Row.ResolutionNote,
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
