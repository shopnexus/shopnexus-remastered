package orderrepo

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	ordermodel "shopnexus-server/internal/module/order/model"
)

const getPayoutSessionForOrder = `-- name: GetPayoutSessionForOrder :one
SELECT s.id, s.kind, s.status, s.from_id, s.to_id, s.note, s.currency, s.total_amount, s.fx_snapshot, s.data, s.date_created, s.date_paid, s.date_expired FROM "order"."payment_session" s
WHERE s."id" = $1 AND s."kind" = 'seller-payout'
LIMIT 1
`

// PayoutWorkflow sets payment_session.id = order.id for the seller-payout
// session (workflow_payout.go:51, sessionID = restate.Key(ctx) = orderID).
// Returns the row regardless of status so callers can render "Funds released"
// when status='Success'. Returns pgx.ErrNoRows if no payout has started.
func (r *Repository) GetPayoutSessionForOrder(ctx context.Context, orderID uuid.UUID) (ordermodel.PaymentSession, error) {
	row := r.db.QueryRow(ctx, getPayoutSessionForOrder, orderID)
	var i ordermodel.PaymentSession
	err := row.Scan(
		&i.ID,
		&i.Kind,
		&i.Status,
		&i.FromID,
		&i.ToID,
		&i.Note,
		&i.Currency,
		&i.TotalAmount,
		&i.FxSnapshot,
		&i.Data,
		&i.DateCreated,
		&i.DatePaid,
		&i.DateExpired,
	)
	return i, err
}

const listCheckoutSiblingsForSession = `-- name: ListCheckoutSiblingsForSession :many
SELECT s2.id, s2.kind, s2.status, s2.from_id, s2.to_id, s2.note, s2.currency, s2.total_amount, s2.fx_snapshot, s2.data, s2.date_created, s2.date_paid, s2.date_expired FROM "order"."payment_session" s1
JOIN "order"."payment_session" s2 ON s2."from_id" = s1."from_id"
    AND s2."kind" = 'buyer-checkout'
    AND abs(extract(epoch from (s2."date_created" - s1."date_created"))) < 2
WHERE s1."id" = $1
`

// Sibling = buyer-checkout sessions with same from_id, within ±2s of the given session.
func (r *Repository) ListCheckoutSiblingsForSession(ctx context.Context, sessionID uuid.UUID) ([]ordermodel.PaymentSession, error) {
	rows, err := r.db.Query(ctx, listCheckoutSiblingsForSession, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ordermodel.PaymentSession{}
	for rows.Next() {
		var i ordermodel.PaymentSession
		if err := rows.Scan(
			&i.ID,
			&i.Kind,
			&i.Status,
			&i.FromID,
			&i.ToID,
			&i.Note,
			&i.Currency,
			&i.TotalAmount,
			&i.FxSnapshot,
			&i.Data,
			&i.DateCreated,
			&i.DatePaid,
			&i.DateExpired,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const listConfirmFeeSiblingsForSession = `-- name: ListConfirmFeeSiblingsForSession :many
SELECT s2.id, s2.kind, s2.status, s2.from_id, s2.to_id, s2.note, s2.currency, s2.total_amount, s2.fx_snapshot, s2.data, s2.date_created, s2.date_paid, s2.date_expired FROM "order"."payment_session" s1
JOIN "order"."payment_session" s2 ON s2."from_id" = s1."from_id"
    AND s2."kind" = 'seller-confirmation-fee'
    AND abs(extract(epoch from (s2."date_created" - s1."date_created"))) < 2
WHERE s1."id" = $1
`

// Sibling = seller-confirmation-fee sessions with same from_id, within ±2s of the given session.
func (r *Repository) ListConfirmFeeSiblingsForSession(ctx context.Context, sessionID uuid.UUID) ([]ordermodel.PaymentSession, error) {
	rows, err := r.db.Query(ctx, listConfirmFeeSiblingsForSession, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ordermodel.PaymentSession{}
	for rows.Next() {
		var i ordermodel.PaymentSession
		if err := rows.Scan(
			&i.ID,
			&i.Kind,
			&i.Status,
			&i.FromID,
			&i.ToID,
			&i.Note,
			&i.Currency,
			&i.TotalAmount,
			&i.FxSnapshot,
			&i.Data,
			&i.DateCreated,
			&i.DatePaid,
			&i.DateExpired,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const listExpiredPendingSessions = `-- name: ListExpiredPendingSessions :many
SELECT id, kind, status, from_id, to_id, note, currency, total_amount, fx_snapshot, data, date_created, date_paid, date_expired FROM "order"."payment_session"
WHERE "status" = 'Pending' AND "date_expired" < $1::TIMESTAMPTZ
ORDER BY "date_expired"
LIMIT $2::INTEGER
`

type ListExpiredPendingSessionsParams struct {
	Cutoff     time.Time `json:"cutoff"`
	LimitCount int32     `json:"limit_count"`
}

func (r *Repository) ListExpiredPendingSessions(ctx context.Context, arg ListExpiredPendingSessionsParams) ([]ordermodel.PaymentSession, error) {
	rows, err := r.db.Query(ctx, listExpiredPendingSessions, arg.Cutoff, arg.LimitCount)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ordermodel.PaymentSession{}
	for rows.Next() {
		var i ordermodel.PaymentSession
		if err := rows.Scan(
			&i.ID,
			&i.Kind,
			&i.Status,
			&i.FromID,
			&i.ToID,
			&i.Note,
			&i.Currency,
			&i.TotalAmount,
			&i.FxSnapshot,
			&i.Data,
			&i.DateCreated,
			&i.DatePaid,
			&i.DateExpired,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const markPaymentSessionCancelled = `-- name: MarkPaymentSessionCancelled :one
UPDATE "order"."payment_session"
SET "status" = 'Cancelled'
WHERE "id" = $1 AND "status" = 'Pending'
RETURNING id, kind, status, from_id, to_id, note, currency, total_amount, fx_snapshot, data, date_created, date_paid, date_expired
`

func (r *Repository) MarkPaymentSessionCancelled(ctx context.Context, id uuid.UUID) (ordermodel.PaymentSession, error) {
	row := r.db.QueryRow(ctx, markPaymentSessionCancelled, id)
	var i ordermodel.PaymentSession
	err := row.Scan(
		&i.ID,
		&i.Kind,
		&i.Status,
		&i.FromID,
		&i.ToID,
		&i.Note,
		&i.Currency,
		&i.TotalAmount,
		&i.FxSnapshot,
		&i.Data,
		&i.DateCreated,
		&i.DatePaid,
		&i.DateExpired,
	)
	return i, err
}

const markPaymentSessionFailed = `-- name: MarkPaymentSessionFailed :one
UPDATE "order"."payment_session"
SET "status" = 'Failed'
WHERE "id" = $1 AND "status" = 'Pending'
RETURNING id, kind, status, from_id, to_id, note, currency, total_amount, fx_snapshot, data, date_created, date_paid, date_expired
`

func (r *Repository) MarkPaymentSessionFailed(ctx context.Context, id uuid.UUID) (ordermodel.PaymentSession, error) {
	row := r.db.QueryRow(ctx, markPaymentSessionFailed, id)
	var i ordermodel.PaymentSession
	err := row.Scan(
		&i.ID,
		&i.Kind,
		&i.Status,
		&i.FromID,
		&i.ToID,
		&i.Note,
		&i.Currency,
		&i.TotalAmount,
		&i.FxSnapshot,
		&i.Data,
		&i.DateCreated,
		&i.DatePaid,
		&i.DateExpired,
	)
	return i, err
}

const markPaymentSessionSuccess = `-- name: MarkPaymentSessionSuccess :one
UPDATE "order"."payment_session"
SET "status" = 'Success',
    "date_paid" = COALESCE($1::TIMESTAMPTZ, CURRENT_TIMESTAMP)
WHERE "id" = $2 AND "status" = 'Pending'
RETURNING id, kind, status, from_id, to_id, note, currency, total_amount, fx_snapshot, data, date_created, date_paid, date_expired
`

type MarkPaymentSessionSuccessParams struct {
	DatePaid time.Time `json:"date_paid"`
	ID       uuid.UUID `json:"id"`
}

func (r *Repository) MarkPaymentSessionSuccess(ctx context.Context, arg MarkPaymentSessionSuccessParams) (ordermodel.PaymentSession, error) {
	row := r.db.QueryRow(ctx, markPaymentSessionSuccess, arg.DatePaid, arg.ID)
	var i ordermodel.PaymentSession
	err := row.Scan(
		&i.ID,
		&i.Kind,
		&i.Status,
		&i.FromID,
		&i.ToID,
		&i.Note,
		&i.Currency,
		&i.TotalAmount,
		&i.FxSnapshot,
		&i.Data,
		&i.DateCreated,
		&i.DatePaid,
		&i.DateExpired,
	)
	return i, err
}

const setPaymentSessionData = `-- name: SetPaymentSessionData :exec


UPDATE "order"."payment_session" SET "data" = $1 WHERE "id" = $2
`

type SetPaymentSessionDataParams struct {
	Data json.RawMessage `json:"data"`
	ID   uuid.UUID       `json:"id"`
}

func (r *Repository) SetPaymentSessionData(ctx context.Context, arg SetPaymentSessionDataParams) error {
	_, err := r.db.Exec(ctx, setPaymentSessionData, arg.Data, arg.ID)
	return err
}
