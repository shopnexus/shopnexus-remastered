package orderrepo

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	null "github.com/guregu/null/v6"
	ordermodel "shopnexus-server/internal/module/order/model"
)

const getLatestGatewayTxBySession = `-- name: GetLatestGatewayTxBySession :one
SELECT id, session_id, status, note, error, payment_option, data, amount, currency, reverses_id, date_created, date_settled, date_expired FROM "order"."transaction"
WHERE "session_id" = $1 AND "payment_option" IS NOT NULL
ORDER BY "date_created" DESC
LIMIT 1
`

// Find the most recent gateway-rail tx for a session (any status). Used by
// the "ensure payment URL" endpoint to decide reuse vs trigger-new-attempt.
// Gateway rail = payment_option IS NOT NULL (wallet txs are NULL).
func (r *Repository) GetLatestGatewayTxBySession(ctx context.Context, sessionID uuid.UUID) (ordermodel.Transaction, error) {
	row := r.db.QueryRow(ctx, getLatestGatewayTxBySession, sessionID)
	var i ordermodel.Transaction
	err := row.Scan(
		&i.ID,
		&i.SessionID,
		&i.Status,
		&i.Note,
		&i.Error,
		&i.PaymentOption,
		&i.Data,
		&i.Amount,
		&i.Currency,
		&i.ReversesID,
		&i.DateCreated,
		&i.DateSettled,
		&i.DateExpired,
	)
	return i, err
}

const listTransactionsByIDs = `-- name: ListTransactionsByIDs :many
SELECT id, session_id, status, note, error, payment_option, data, amount, currency, reverses_id, date_created, date_settled, date_expired FROM "order"."transaction" WHERE "id" = ANY($1::BIGINT[])
`

func (r *Repository) ListTransactionsByIDs(ctx context.Context, ids []int64) ([]ordermodel.Transaction, error) {
	rows, err := r.db.Query(ctx, listTransactionsByIDs, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ordermodel.Transaction{}
	for rows.Next() {
		var i ordermodel.Transaction
		if err := rows.Scan(
			&i.ID,
			&i.SessionID,
			&i.Status,
			&i.Note,
			&i.Error,
			&i.PaymentOption,
			&i.Data,
			&i.Amount,
			&i.Currency,
			&i.ReversesID,
			&i.DateCreated,
			&i.DateSettled,
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

const listTransactionsByItem = `-- name: ListTransactionsByItem :many
SELECT t.id, t.session_id, t.status, t.note, t.error, t.payment_option, t.data, t.amount, t.currency, t.reverses_id, t.date_created, t.date_settled, t.date_expired FROM "order"."transaction" t
WHERE t."session_id" = (SELECT i."payment_session_id" FROM "order"."item" i WHERE i."id" = $1)
ORDER BY t."date_created"
`

func (r *Repository) ListTransactionsByItem(ctx context.Context, itemID int64) ([]ordermodel.Transaction, error) {
	rows, err := r.db.Query(ctx, listTransactionsByItem, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ordermodel.Transaction{}
	for rows.Next() {
		var i ordermodel.Transaction
		if err := rows.Scan(
			&i.ID,
			&i.SessionID,
			&i.Status,
			&i.Note,
			&i.Error,
			&i.PaymentOption,
			&i.Data,
			&i.Amount,
			&i.Currency,
			&i.ReversesID,
			&i.DateCreated,
			&i.DateSettled,
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

const listTransactionsByOrder = `-- name: ListTransactionsByOrder :many
SELECT t.id, t.session_id, t.status, t.note, t.error, t.payment_option, t.data, t.amount, t.currency, t.reverses_id, t.date_created, t.date_settled, t.date_expired FROM "order"."transaction" t
WHERE t."session_id" IN (
    SELECT o."confirm_session_id" FROM "order"."order" o WHERE o."id" = $1
    UNION
    SELECT i."payment_session_id" FROM "order"."item" i WHERE i."order_id" = $1
)
ORDER BY t."date_created"
`

func (r *Repository) ListTransactionsByOrder(ctx context.Context, orderID uuid.UUID) ([]ordermodel.Transaction, error) {
	rows, err := r.db.Query(ctx, listTransactionsByOrder, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ordermodel.Transaction{}
	for rows.Next() {
		var i ordermodel.Transaction
		if err := rows.Scan(
			&i.ID,
			&i.SessionID,
			&i.Status,
			&i.Note,
			&i.Error,
			&i.PaymentOption,
			&i.Data,
			&i.Amount,
			&i.Currency,
			&i.ReversesID,
			&i.DateCreated,
			&i.DateSettled,
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

const listTransactionsBySession = `-- name: ListTransactionsBySession :many
SELECT id, session_id, status, note, error, payment_option, data, amount, currency, reverses_id, date_created, date_settled, date_expired FROM "order"."transaction"
WHERE "session_id" = $1
ORDER BY "date_created"
`

func (r *Repository) ListTransactionsBySession(ctx context.Context, sessionID uuid.UUID) ([]ordermodel.Transaction, error) {
	rows, err := r.db.Query(ctx, listTransactionsBySession, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ordermodel.Transaction{}
	for rows.Next() {
		var i ordermodel.Transaction
		if err := rows.Scan(
			&i.ID,
			&i.SessionID,
			&i.Status,
			&i.Note,
			&i.Error,
			&i.PaymentOption,
			&i.Data,
			&i.Amount,
			&i.Currency,
			&i.ReversesID,
			&i.DateCreated,
			&i.DateSettled,
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

const markPendingTxsFailedBySession = `-- name: MarkPendingTxsFailedBySession :exec
UPDATE "order"."transaction"
SET "status" = 'Failed',
    "error" = $1
WHERE "session_id" = $2 AND "status" = 'Pending'
`

type MarkPendingTxsFailedBySessionParams struct {
	Error     null.String `json:"error"`
	SessionID uuid.UUID   `json:"session_id"`
}

// Saga-compensator for multi-attempt sessions: mark every still-Pending tx
// in the session as Failed. Idempotent (only Pending rows are touched).
func (r *Repository) MarkPendingTxsFailedBySession(ctx context.Context, arg MarkPendingTxsFailedBySessionParams) error {
	_, err := r.db.Exec(ctx, markPendingTxsFailedBySession, arg.Error, arg.SessionID)
	return err
}

const markTransactionCancelled = `-- name: MarkTransactionCancelled :one
UPDATE "order"."transaction"
SET "status" = 'Cancelled'
WHERE "id" = $1 AND "status" = 'Pending'
RETURNING id, session_id, status, note, error, payment_option, data, amount, currency, reverses_id, date_created, date_settled, date_expired
`

func (r *Repository) MarkTransactionCancelled(ctx context.Context, id uuid.UUID) (ordermodel.Transaction, error) {
	row := r.db.QueryRow(ctx, markTransactionCancelled, id)
	var i ordermodel.Transaction
	err := row.Scan(
		&i.ID,
		&i.SessionID,
		&i.Status,
		&i.Note,
		&i.Error,
		&i.PaymentOption,
		&i.Data,
		&i.Amount,
		&i.Currency,
		&i.ReversesID,
		&i.DateCreated,
		&i.DateSettled,
		&i.DateExpired,
	)
	return i, err
}

const markTransactionSuccess = `-- name: MarkTransactionSuccess :one
UPDATE "order"."transaction"
SET "status" = 'Success',
    "date_settled" = COALESCE($1::TIMESTAMPTZ, CURRENT_TIMESTAMP)
WHERE "id" = $2 AND "status" = 'Pending'
RETURNING id, session_id, status, note, error, payment_option, data, amount, currency, reverses_id, date_created, date_settled, date_expired
`

type MarkTransactionSuccessParams struct {
	DateSettled time.Time `json:"date_settled"`
	ID          uuid.UUID `json:"id"`
}

func (r *Repository) MarkTransactionSuccess(ctx context.Context, arg MarkTransactionSuccessParams) (ordermodel.Transaction, error) {
	row := r.db.QueryRow(ctx, markTransactionSuccess, arg.DateSettled, arg.ID)
	var i ordermodel.Transaction
	err := row.Scan(
		&i.ID,
		&i.SessionID,
		&i.Status,
		&i.Note,
		&i.Error,
		&i.PaymentOption,
		&i.Data,
		&i.Amount,
		&i.Currency,
		&i.ReversesID,
		&i.DateCreated,
		&i.DateSettled,
		&i.DateExpired,
	)
	return i, err
}

const markTransactionsFailed = `-- name: MarkTransactionsFailed :exec
UPDATE "order"."transaction"
SET "status" = 'Failed',
    "error" = $1
WHERE "id" = ANY($2) AND "status" = 'Pending'
`

type MarkTransactionsFailedParams struct {
	Error null.String `json:"error"`
	ID    []uuid.UUID `json:"id"`
}

func (r *Repository) MarkTransactionsFailed(ctx context.Context, arg MarkTransactionsFailedParams) error {
	_, err := r.db.Exec(ctx, markTransactionsFailed, arg.Error, arg.ID)
	return err
}

const setTransactionData = `-- name: SetTransactionData :exec


UPDATE "order"."transaction" SET "data" = $1 WHERE "id" = $2
`

type SetTransactionDataParams struct {
	Data json.RawMessage `json:"data"`
	ID   uuid.UUID       `json:"id"`
}

func (r *Repository) SetTransactionData(ctx context.Context, arg SetTransactionDataParams) error {
	_, err := r.db.Exec(ctx, setTransactionData, arg.Data, arg.ID)
	return err
}
