// Package postgres implements the trust port.Repository using pgx named args.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopnexus/internal/module/common/dbx"
	"shopnexus/internal/module/trust/domain"
	"shopnexus/internal/module/trust/port"
)

type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

var _ port.Repository = (*Repo)(nil)

func (r *Repo) InsertFeedback(ctx context.Context, f *domain.Feedback) error {
	const q = `INSERT INTO feedback (order_id, rater_id, ratee_id, direction, rating, comment)
	           VALUES (@order_id, @rater_id, @ratee_id, @direction, @rating, @comment)
	           RETURNING id, created_at, published_at`
	args := pgx.NamedArgs{
		"order_id":  f.OrderID,
		"rater_id":  f.RaterID,
		"ratee_id":  f.RateeID,
		"direction": f.Direction,
		"rating":    f.Rating,
		"comment":   f.Comment,
	}
	if err := r.pool.QueryRow(ctx, q, args).Scan(&f.ID, &f.CreatedAt, &f.PublishedAt); err != nil {
		if dbx.IsUniqueViolation(err) {
			return domain.ErrFeedbackExists
		}
		return fmt.Errorf("db insert feedback: %w", err)
	}
	return nil
}

func (r *Repo) FindReputation(ctx context.Context, accountID int64, role string) (domain.Reputation, error) {
	const q = `SELECT account_id, role, rating_sum, rating_count, completed_orders, cancelled_orders, updated_at
	           FROM reputation WHERE account_id = @account_id AND role = @role`
	var rep domain.Reputation
	err := r.pool.QueryRow(ctx, q, pgx.NamedArgs{"account_id": accountID, "role": role}).
		Scan(&rep.AccountID, &rep.Role, &rep.RatingSum, &rep.RatingCount,
			&rep.CompletedOrders, &rep.CancelledOrders, &rep.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Reputation{}, domain.ErrReputationNotFound
	}
	if err != nil {
		return domain.Reputation{}, fmt.Errorf("db scan reputation: %w", err)
	}
	return rep, nil
}

func (r *Repo) InsertReport(ctx context.Context, rep *domain.Report) error {
	const q = `INSERT INTO report (reporter_id, ref_type, ref_id, reason, detail, status)
	           VALUES (@reporter_id, @ref_type, @ref_id, @reason, @detail, @status)
	           RETURNING id, created_at`
	args := pgx.NamedArgs{
		"reporter_id": rep.ReporterID,
		"ref_type":    rep.RefType,
		"ref_id":      rep.RefID,
		"reason":      rep.Reason,
		"detail":      rep.Detail,
		"status":      rep.Status,
	}
	if err := r.pool.QueryRow(ctx, q, args).Scan(&rep.ID, &rep.CreatedAt); err != nil {
		if dbx.IsUniqueViolation(err) {
			return domain.ErrReportExists
		}
		return fmt.Errorf("db insert report: %w", err)
	}
	return nil
}
