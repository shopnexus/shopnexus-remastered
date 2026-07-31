package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/common/dbx"
	"shopnexus/internal/module/trust/domain"
	"shopnexus/internal/module/trust/port"
)

const reportColumns = `id, reporter_id, ref_type::text, ref_id, reason::text, detail,
	       status::text, action_taken::text, resolved_by_id, resolved_at, resolution_note,
	       created_at`

func scanReport(row pgx.Row) (domain.Report, error) {
	var v domain.Report
	err := row.Scan(&v.ID, &v.ReporterID, &v.RefType, &v.RefID, &v.Reason, &v.Detail,
		&v.Status, &v.ActionTaken, &v.ResolvedByID, &v.ResolvedAt, &v.ResolutionNote,
		&v.CreatedAt)
	if dbx.IsNoRows(err) {
		return domain.Report{}, domain.ErrReportNotFound
	}
	if err != nil {
		return domain.Report{}, fmt.Errorf("db scan report: %w", err)
	}
	return v, nil
}

func (r *Repo) InsertReport(ctx context.Context, v *domain.Report) error {
	const q = `INSERT INTO report (reporter_id, ref_type, ref_id, reason, detail, status)
	           VALUES (@reporter_id, @ref_type, @ref_id, @reason, @detail, @status)
	           RETURNING id, created_at`
	args := pgx.NamedArgs{
		"reporter_id": v.ReporterID, "ref_type": v.RefType, "ref_id": v.RefID,
		"reason": v.Reason, "detail": v.Detail, "status": v.Status,
	}
	if err := r.pool.QueryRow(ctx, q, args).Scan(&v.ID, &v.CreatedAt); err != nil {
		if dbx.IsUniqueViolation(err) {
			return domain.ErrReportExists
		}
		return fmt.Errorf("db insert report: %w", err)
	}
	return nil
}

func (r *Repo) FindReport(ctx context.Context, id int64) (domain.Report, error) {
	const q = `SELECT ` + reportColumns + ` FROM report WHERE id = @id`
	return scanReport(r.pool.QueryRow(ctx, q, pgx.NamedArgs{"id": id}))
}

// ListReports serves both the reporter's own list and the moderator queue. The queue is
// worked oldest first — the order the partial index delivers — and a reporter reads their
// own newest first, because that is a history rather than a work list.
func (r *Repo) ListReports(ctx context.Context, f port.ReportFilter) ([]domain.Report, error) {
	const base = `SELECT ` + reportColumns + ` FROM report
	           WHERE (@reporter_id = 0 OR reporter_id = @reporter_id)
	             AND (@statuses::text[] IS NULL OR status::text = ANY(@statuses::text[]))
	             AND (@ref_type::text IS NULL OR ref_type = @ref_type::report_ref_type)
	             AND (@reason::text IS NULL OR reason = @reason::report_reason)`
	q := base + ` AND (@before::timestamptz IS NULL OR created_at < @before::timestamptz)
	              ORDER BY created_at DESC, id DESC LIMIT @limit`
	if f.ReporterID == 0 {
		q = base + ` AND (@before::timestamptz IS NULL OR created_at > @before::timestamptz)
		              ORDER BY created_at, id LIMIT @limit`
	}
	before, limit := cursorBound(f.Cursor)
	args := pgx.NamedArgs{
		"reporter_id": f.ReporterID, "statuses": nullStrings(f.Statuses),
		"ref_type": nullText(f.RefType), "reason": nullText(f.Reason),
		"before": before, "limit": limit,
	}
	rows, err := r.pool.Query(ctx, q, args)
	if err != nil {
		return nil, fmt.Errorf("db query reports: %w", err)
	}
	defer rows.Close()
	var out []domain.Report
	for rows.Next() {
		v, err := scanReport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate reports: %w", err)
	}
	return out, nil
}

// SaveReport writes a claim or a verdict, guarded by the status it moves from: a stale read
// loses instead of overwriting a decision it never saw.
func (r *Repo) SaveReport(ctx context.Context, v domain.Report, from []string) error {
	const q = `UPDATE report
	           SET status = @status, action_taken = @action_taken,
	               resolved_by_id = @resolved_by, resolved_at = @resolved_at,
	               resolution_note = @note
	           WHERE id = @id AND status::text = ANY(@from::text[])`
	args := pgx.NamedArgs{
		"id": v.ID, "status": v.Status, "action_taken": v.ActionTaken,
		"resolved_by": v.ResolvedByID, "resolved_at": v.ResolvedAt,
		"note": v.ResolutionNote, "from": from,
	}
	tag, err := r.pool.Exec(ctx, q, args)
	if err != nil {
		return fmt.Errorf("db update report: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrReportResolved
	}
	return nil
}

// CountOpenAgainst is how many unresolved reports name the same target — the pattern a
// decision rests on rather than one complaint.
func (r *Repo) CountOpenAgainst(ctx context.Context, refType string, refID int64) (int64, error) {
	const q = `SELECT COUNT(*) FROM report
	           WHERE ref_type = @ref_type AND ref_id = @ref_id
	             AND status IN ('open', 'reviewing')`
	var count int64
	err := r.pool.QueryRow(ctx, q, pgx.NamedArgs{"ref_type": refType, "ref_id": refID}).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("db count open reports: %w", err)
	}
	return count, nil
}

func nullStrings(v []string) any {
	if len(v) == 0 {
		return nil
	}
	return v
}
