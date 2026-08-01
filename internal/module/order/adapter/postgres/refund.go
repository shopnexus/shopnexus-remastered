package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/common/dbx"
	"shopnexus/internal/module/order/domain"
	"shopnexus/internal/module/order/port"
)

const refundColumns = `id, buyer_id, order_id, reason, attachments, created_at,
	       status::text, deadline_at, seller_decided_at, rejection_reason,
	       return_transport_id, returned_at`

func scanRefund(row pgx.Row) (domain.Refund, error) {
	var r domain.Refund
	err := row.Scan(&r.ID, &r.BuyerID, &r.OrderID, &r.Reason, &r.Attachments, &r.CreatedAt,
		&r.Status, &r.DeadlineAt, &r.SellerDecidedAt, &r.RejectionReason,
		&r.ReturnTransportID, &r.ReturnedAt)
	if dbx.IsNoRows(err) {
		return domain.Refund{}, domain.ErrRefundNotFound
	}
	if err != nil {
		return domain.Refund{}, fmt.Errorf("db scan refund: %w", err)
	}
	return r, nil
}

// InsertRefund opens a case. One live refund per order — a refund covers the whole order, so a
// second could not be about anything — held by the partial unique index.
//
// Under the order's advisory lock, and only over an order that is still open. The service's own
// `o.Settled()` check is a read, and so is ClaimPayout's mirror of it: without the lock the
// payout sweep releases the escrow to the seller while this row lands, and the verdict then
// refunds the buyer out of a hold that has already gone somewhere else.
func (r *Repo) InsertRefund(ctx context.Context, ref *domain.Refund) error {
	return dbx.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		const lock = `SELECT pg_advisory_xact_lock(@space, @order_id)`
		if _, err := tx.Exec(ctx, lock, pgx.NamedArgs{
			"space": int64(orderEscrowLock), "order_id": ref.OrderID,
		}); err != nil {
			return fmt.Errorf("db lock order: %w", err)
		}
		const q = `INSERT INTO refund (buyer_id, order_id, reason, attachments, status, deadline_at)
		           SELECT @buyer_id, @order_id, @reason, @attachments, @status, @deadline_at
		           WHERE EXISTS (
		             SELECT 1 FROM "order"
		             WHERE id = @order_id AND completed_at IS NULL AND cancelled_at IS NULL
		           )
		           RETURNING id, created_at`
		args := pgx.NamedArgs{
			"buyer_id": ref.BuyerID, "order_id": ref.OrderID, "reason": ref.Reason,
			"attachments": dbx.Int64Array(ref.Attachments), "status": ref.Status,
			"deadline_at": ref.DeadlineAt,
		}
		err := tx.QueryRow(ctx, q, args).Scan(&ref.ID, &ref.CreatedAt)
		if dbx.IsNoRows(err) {
			// The escrow this refund is about has already been paid out or sent back.
			return domain.ErrRefundNotDue
		}
		if err != nil {
			if dbx.IsUniqueViolation(err) {
				return domain.ErrRefundAlreadyOpen
			}
			return fmt.Errorf("db insert refund: %w", err)
		}
		return nil
	})
}

func (r *Repo) FindRefund(ctx context.Context, id int64) (domain.Refund, error) {
	const q = `SELECT ` + refundColumns + ` FROM refund WHERE id = @id`
	return scanRefund(r.pool.QueryRow(ctx, q, pgx.NamedArgs{"id": id}))
}

// terminalRefund is every status a case can end on. Spelled once, because a transition guard
// that forgets one lets a settled case move again.
const terminalRefund = `'` + domain.RefundAccepted + `', '` + domain.RefundRejected + `', '` + domain.RefundCancelled + `'`

func (r *Repo) ListRefunds(ctx context.Context, f port.RefundFilter) ([]domain.Refund, error) {
	// A seller's refunds are the ones on their orders, which is a join rather than a
	// column: the refund belongs to the buyer who raised it.
	const q = `SELECT ` + refundColumns + ` FROM refund r
	           WHERE (@buyer_id = 0 OR r.buyer_id = @buyer_id)
	             AND (@seller_id = 0 OR EXISTS (
	                   SELECT 1 FROM "order" o
	                   WHERE o.id = r.order_id AND o.seller_id = @seller_id))
	             AND (@statuses::text[] IS NULL OR r.status::text = ANY(@statuses::text[]))
	             AND (@before::timestamptz IS NULL
	                  OR (r.created_at, r.id) < (@before::timestamptz, @before_id::bigint))
	           ORDER BY r.created_at DESC, r.id DESC
	           LIMIT @limit`
	before, beforeID, limit := cursorBound(f.Cursor)
	args := pgx.NamedArgs{
		"buyer_id": f.BuyerID, "seller_id": f.SellerID,
		"statuses": nullStrings(f.Statuses), "before": before, "before_id": beforeID,
		"limit": limit,
	}
	return r.queryRefunds(ctx, q, args)
}

// OverdueRefunds advances all three windows with one query — which is what naming every
// non-terminal status for the party it waits on buys. The two states that wait on a carrier
// or a moderator carry no deadline, so they are excluded by the column being NULL.
func (r *Repo) OverdueRefunds(ctx context.Context, now time.Time, limit int) ([]domain.Refund, error) {
	const q = `SELECT ` + refundColumns + ` FROM refund
	           WHERE status NOT IN (` + terminalRefund + `)
	             AND deadline_at IS NOT NULL AND deadline_at < @now
	           ORDER BY deadline_at
	           LIMIT @limit`
	return r.queryRefunds(ctx, q, pgx.NamedArgs{"now": now, "limit": limit})
}

func (r *Repo) queryRefunds(ctx context.Context, q string, args pgx.NamedArgs) ([]domain.Refund, error) {
	rows, err := r.pool.Query(ctx, q, args)
	if err != nil {
		return nil, fmt.Errorf("db query refunds: %w", err)
	}
	defer rows.Close()

	var out []domain.Refund
	for rows.Next() {
		ref, err := scanRefund(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate refunds: %w", err)
	}
	return out, nil
}

// SaveRefund writes the transition. The status it moves *from* is in the WHERE clause, so
// two moves on one refund cannot both land — the loser is told the case moved on.
func (r *Repo) SaveRefund(ctx context.Context, ref domain.Refund) error {
	tag, err := r.pool.Exec(ctx, saveRefund, refundArgs(ref))
	if err != nil {
		return fmt.Errorf("db update refund: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrRefundSettled
	}
	return nil
}

// SaveRefundOutcome writes a refund transition with the rows that transition decides: the
// dispute round whose verdict it is, and the order it closes. One transaction, because the two
// halves apart are each a state nothing can get out of — a ruled round over a still-disputed
// refund is a dead end, and a settled refund over an order still open is money the payout sweep
// would hand the seller after the buyer has already been paid.
func (r *Repo) SaveRefundOutcome(ctx context.Context, ref domain.Refund, d *domain.Dispute, o *domain.Order) error {
	return dbx.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, saveRefund, refundArgs(ref))
		if err != nil {
			return fmt.Errorf("db update refund: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrRefundSettled
		}
		if d != nil {
			tag, err := tx.Exec(ctx, saveDispute, disputeArgs(*d))
			if err != nil {
				return fmt.Errorf("db update dispute: %w", err)
			}
			if tag.RowsAffected() == 0 {
				return domain.ErrDisputeSettled
			}
		}
		if o != nil {
			// The order's lock, so the close is serialised against the payout claim exactly as
			// the refund's own insert was.
			const lock = `SELECT pg_advisory_xact_lock(@space, @order_id)`
			if _, err := tx.Exec(ctx, lock, pgx.NamedArgs{
				"space": int64(orderEscrowLock), "order_id": o.ID,
			}); err != nil {
				return fmt.Errorf("db lock order: %w", err)
			}
			tag, err := tx.Exec(ctx, saveOrder, orderArgs(*o))
			if err != nil {
				return fmt.Errorf("db update order: %w", err)
			}
			if tag.RowsAffected() == 0 {
				return domain.ErrOrderSettled
			}
		}
		return nil
	})
}

const saveRefund = `UPDATE refund
                    SET status = @status, deadline_at = @deadline_at,
                        attachments = @attachments,
                        seller_decided_at = @seller_decided_at,
                        rejection_reason = @rejection_reason,
                        return_transport_id = @return_transport_id,
                        returned_at = @returned_at
                    WHERE id = @id AND status NOT IN (` + terminalRefund + `)`

func refundArgs(ref domain.Refund) pgx.NamedArgs {
	return pgx.NamedArgs{
		"id": ref.ID, "status": ref.Status, "deadline_at": ref.DeadlineAt,
		"attachments":       dbx.Int64Array(ref.Attachments),
		"seller_decided_at": ref.SellerDecidedAt, "rejection_reason": ref.RejectionReason,
		"return_transport_id": ref.ReturnTransportID, "returned_at": ref.ReturnedAt,
	}
}

const disputeColumns = `id, refund_id, round, opened_by_id, reason, status::text,
	       resolved_by_id, resolved_at, resolution_note, created_at`

func scanDispute(row pgx.Row) (domain.Dispute, error) {
	var (
		d    domain.Dispute
		note *string
	)
	err := row.Scan(&d.ID, &d.RefundID, &d.Round, &d.OpenedBy, &d.Reason, &d.Status,
		&d.RuledBy, &d.RuledAt, &note, &d.CreatedAt)
	if dbx.IsNoRows(err) {
		return domain.Dispute{}, domain.ErrDisputeNotFound
	}
	if err != nil {
		return domain.Dispute{}, fmt.Errorf("db scan dispute: %w", err)
	}
	if note != nil {
		d.Note = *note
	}
	return d, nil
}

// InsertDispute opens a round. One row per (refund, round), so escalating twice in the same
// round is a conflict rather than two cases.
func (r *Repo) InsertDispute(ctx context.Context, d *domain.Dispute) error {
	const q = `INSERT INTO refund_dispute (refund_id, round, opened_by_id, reason, status)
	           VALUES (@refund_id, @round, @opened_by, @reason, @status)
	           RETURNING id, created_at`
	args := pgx.NamedArgs{
		"refund_id": d.RefundID, "round": d.Round, "opened_by": d.OpenedBy,
		"reason": d.Reason, "status": d.Status,
	}
	if err := r.pool.QueryRow(ctx, q, args).Scan(&d.ID, &d.CreatedAt); err != nil {
		if dbx.IsUniqueViolation(err) {
			return domain.ErrDisputeSettled
		}
		return fmt.Errorf("db insert dispute: %w", err)
	}
	return nil
}

func (r *Repo) FindDispute(ctx context.Context, id int64) (domain.Dispute, error) {
	const q = `SELECT ` + disputeColumns + ` FROM refund_dispute WHERE id = @id`
	return scanDispute(r.pool.QueryRow(ctx, q, pgx.NamedArgs{"id": id}))
}

// ListOpenDisputes is the moderator queue, oldest first — the order it should be worked.
func (r *Repo) ListOpenDisputes(ctx context.Context, f port.CursorFilter) ([]domain.Dispute, error) {
	const q = `SELECT ` + disputeColumns + ` FROM refund_dispute
	           WHERE status = '` + domain.DisputeOpen + `'
	             AND (@before::timestamptz IS NULL
	                  OR (created_at, id) > (@before::timestamptz, @before_id::bigint))
	           ORDER BY created_at, id
	           LIMIT @limit`
	before, beforeID, limit := cursorBound(f)
	args := pgx.NamedArgs{"before": before, "before_id": beforeID, "limit": limit}
	rows, err := r.pool.Query(ctx, q, args)
	if err != nil {
		return nil, fmt.Errorf("db query disputes: %w", err)
	}
	defer rows.Close()

	var out []domain.Dispute
	for rows.Next() {
		d, err := scanDispute(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate disputes: %w", err)
	}
	return out, nil
}

// saveDispute records the verdict. `WHERE status = 'open'` is the transition: a round is ruled
// once, because a later round is argued against what the earlier one decided. Only ever run
// beside the refund it moved — see SaveRefundOutcome.
const saveDispute = `UPDATE refund_dispute
                     SET status = @status, resolved_by_id = @resolved_by,
                         resolved_at = @resolved_at, resolution_note = @note
                     WHERE id = @id AND status = '` + domain.DisputeOpen + `'`

func disputeArgs(d domain.Dispute) pgx.NamedArgs {
	return pgx.NamedArgs{
		"id": d.ID, "status": d.Status, "resolved_by": d.RuledBy,
		"resolved_at": d.RuledAt, "note": dbx.NullText(d.Note),
	}
}

func nullStrings(v []string) any {
	if len(v) == 0 {
		return nil
	}
	return v
}
