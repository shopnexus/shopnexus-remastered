package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/common/dbx"
	"shopnexus/internal/module/trust/domain"
	"shopnexus/internal/module/trust/port"
)

const ticketColumns = `id, requester_id, kind::text, subject, ref_type::text, ref_id,
	       reason::text, status::text, assignee_id, conversation_id,
	       action_taken::text, resolved_by_id, resolved_at, resolution_note, created_at`

func scanTicket(row pgx.Row) (domain.Ticket, error) {
	var v domain.Ticket
	err := row.Scan(&v.ID, &v.RequesterID, &v.Kind, &v.Subject, &v.RefType, &v.RefID,
		&v.Reason, &v.Status, &v.AssigneeID, &v.ConversationID,
		&v.ActionTaken, &v.ResolvedByID, &v.ResolvedAt, &v.ResolutionNote, &v.CreatedAt)
	if dbx.IsNoRows(err) {
		return domain.Ticket{}, domain.ErrTicketNotFound
	}
	if err != nil {
		return domain.Ticket{}, fmt.Errorf("db scan ticket: %w", err)
	}
	return v, nil
}

func (r *Repo) InsertTicket(ctx context.Context, v *domain.Ticket) error {
	const q = `INSERT INTO ticket (requester_id, kind, subject, ref_type, ref_id, reason, status)
	           VALUES (@requester_id, @kind, @subject, @ref_type, @ref_id, @reason, @status)
	           RETURNING id, created_at`
	args := pgx.NamedArgs{
		"requester_id": v.RequesterID, "kind": v.Kind, "subject": v.Subject,
		"ref_type": v.RefType, "ref_id": v.RefID, "reason": v.Reason, "status": v.Status,
	}
	if err := r.pool.QueryRow(ctx, q, args).Scan(&v.ID, &v.CreatedAt); err != nil {
		if dbx.IsUniqueViolation(err) {
			return domain.ErrTicketExists
		}
		return fmt.Errorf("db insert ticket: %w", err)
	}
	return nil
}

func (r *Repo) FindTicket(ctx context.Context, id int64) (domain.Ticket, error) {
	const q = `SELECT ` + ticketColumns + ` FROM ticket WHERE id = @id`
	return scanTicket(r.pool.QueryRow(ctx, q, pgx.NamedArgs{"id": id}))
}

// FindTicketByRef answers the open ticket about one target, which is how a module that already knows
// the target — order, holding a refund — finds the ticket to close when it decides.
func (r *Repo) FindTicketByRef(ctx context.Context, refType string, refID int64) (domain.Ticket, error) {
	const q = `SELECT ` + ticketColumns + ` FROM ticket
	           WHERE ref_type::text = @ref_type AND ref_id = @ref_id
	             AND status IN ('` + domain.StatusOpen + `', '` + domain.StatusReviewing + `')
	           ORDER BY created_at DESC, id DESC
	           LIMIT 1`
	return scanTicket(r.pool.QueryRow(ctx, q, pgx.NamedArgs{"ref_type": refType, "ref_id": refID}))
}

// ListTickets serves both the requester's own list and the moderator queue. The queue is
// worked oldest first — the order the partial index delivers — and a requester reads their own
// newest first, because that is a history rather than a work list.
func (r *Repo) ListTickets(ctx context.Context, f port.TicketFilter) ([]domain.Ticket, error) {
	const base = `SELECT ` + ticketColumns + ` FROM ticket
	           WHERE (@requester_id = 0 OR requester_id = @requester_id)
	             AND (@statuses::text[] IS NULL OR status::text = ANY(@statuses::text[]))
	             AND (@kind::text IS NULL OR kind::text = @kind::text)
	             AND (@ref_type::text IS NULL OR ref_type::text = @ref_type::text)`
	q := base + ` AND (@before_id = 0
	                   OR (created_at, id) < (@before::timestamptz, @before_id))
	              ORDER BY created_at DESC, id DESC LIMIT @limit`
	if f.RequesterID == 0 {
		q = base + ` AND (@before_id = 0
		                  OR (created_at, id) > (@before::timestamptz, @before_id))
		             ORDER BY created_at, id LIMIT @limit`
	}
	args := pgx.NamedArgs{
		"requester_id": f.RequesterID, "statuses": nullStrings(f.Statuses),
		"kind": dbx.NullText(f.Kind), "ref_type": dbx.NullText(f.RefType),
	}
	addCursor(args, f.Cursor)
	rows, err := r.pool.Query(ctx, q, args)
	if err != nil {
		return nil, fmt.Errorf("db query tickets: %w", err)
	}
	defer rows.Close()
	var out []domain.Ticket
	for rows.Next() {
		v, err := scanTicket(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate tickets: %w", err)
	}
	return out, nil
}

// SaveTicket writes a claim, a verdict or the thread it belongs to, guarded by the status it moves
// from: a stale read loses instead of overwriting a decision it never saw.
func (r *Repo) SaveTicket(ctx context.Context, v domain.Ticket, from []string) error {
	const q = `UPDATE ticket
	           SET status = @status, assignee_id = @assignee_id,
	               conversation_id = COALESCE(@conversation_id, conversation_id),
	               action_taken = @action_taken,
	               resolved_by_id = @resolved_by, resolved_at = @resolved_at,
	               resolution_note = @note
	           WHERE id = @id AND status::text = ANY(@from::text[])`
	args := pgx.NamedArgs{
		"id": v.ID, "status": v.Status, "assignee_id": v.AssigneeID,
		"conversation_id": v.ConversationID, "action_taken": v.ActionTaken,
		"resolved_by": v.ResolvedByID, "resolved_at": v.ResolvedAt,
		"note": v.ResolutionNote, "from": from,
	}
	tag, err := r.pool.Exec(ctx, q, args)
	if err != nil {
		return fmt.Errorf("db update ticket: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrTicketResolved
	}
	return nil
}

// CountOpenAgainst is how many unresolved tickets name each target — the pattern a decision
// rests on rather than one complaint. A whole page of targets in one query: the moderator
// queue asks this per row, and twenty round trips to answer twenty small counts is the shape
// this replaces. A target nobody else reported is absent from the map, which reads as 0.
func (r *Repo) CountOpenAgainst(ctx context.Context, targets []port.TicketTarget) (map[port.TicketTarget]int64, error) {
	out := make(map[port.TicketTarget]int64, len(targets))
	if len(targets) == 0 {
		return out, nil
	}
	refTypes := make([]string, 0, len(targets))
	refIDs := make([]int64, 0, len(targets))
	for _, target := range targets {
		refTypes = append(refTypes, target.RefType)
		refIDs = append(refIDs, target.RefID)
	}
	const q = `SELECT ref_type::text, ref_id, COUNT(*) FROM ticket
	           WHERE (ref_type::text, ref_id) IN (
	                   SELECT * FROM unnest(@ref_types::text[], @ref_ids::bigint[])
	                 )
	             AND status IN ('open', 'reviewing')
	           GROUP BY ref_type, ref_id`
	rows, err := r.pool.Query(ctx, q, pgx.NamedArgs{"ref_types": refTypes, "ref_ids": refIDs})
	if err != nil {
		return nil, fmt.Errorf("db count open tickets: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var target port.TicketTarget
		var count int64
		if err := rows.Scan(&target.RefType, &target.RefID, &count); err != nil {
			return nil, fmt.Errorf("db scan open ticket count: %w", err)
		}
		out[target] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate open ticket counts: %w", err)
	}
	return out, nil
}

func nullStrings(v []string) any {
	if len(v) == 0 {
		return nil
	}
	return v
}
