package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/common/dbx"
	"shopnexus/internal/module/trust/domain"
	"shopnexus/internal/module/trust/port"
)

const feedbackColumns = `id, order_id, rater_id, ratee_id, direction::text, rating, comment,
	       created_at, published_at`

func scanFeedback(row pgx.Row) (domain.Feedback, error) {
	var f domain.Feedback
	err := row.Scan(&f.ID, &f.OrderID, &f.RaterID, &f.RateeID, &f.Direction, &f.Rating,
		&f.Comment, &f.CreatedAt, &f.PublishedAt)
	if dbx.IsNoRows(err) {
		return domain.Feedback{}, domain.ErrFeedbackNotFound
	}
	if err != nil {
		return domain.Feedback{}, fmt.Errorf("db scan feedback: %w", err)
	}
	return f, nil
}

// InsertFeedback writes one direction's rating, and reveals the pair when this submission
// completes it — both rows and both reputations in one transaction, because a reveal that
// lands half-applied shows one side a rating the other cannot see yet.
func (r *Repo) InsertFeedback(ctx context.Context, f *domain.Feedback) error {
	return dbx.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		const q = `INSERT INTO feedback (order_id, rater_id, ratee_id, direction, rating, comment)
		           VALUES (@order_id, @rater_id, @ratee_id, @direction, @rating, @comment)
		           RETURNING id, created_at`
		args := pgx.NamedArgs{
			"order_id": f.OrderID, "rater_id": f.RaterID, "ratee_id": f.RateeID,
			"direction": f.Direction, "rating": f.Rating, "comment": f.Comment,
		}
		if err := tx.QueryRow(ctx, q, args).Scan(&f.ID, &f.CreatedAt); err != nil {
			if dbx.IsUniqueViolation(err) {
				return domain.ErrFeedbackExists
			}
			return fmt.Errorf("db insert feedback: %w", err)
		}
		// The other direction, if it is there. Only then does either become visible.
		const other = `SELECT ` + feedbackColumns + ` FROM feedback
		               WHERE order_id = @order_id AND direction = @direction`
		counterpart, err := scanFeedback(tx.QueryRow(ctx, other, pgx.NamedArgs{
			"order_id": f.OrderID, "direction": domain.Opposite(f.Direction),
		}))
		if errors.Is(err, domain.ErrFeedbackNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		now := time.Now()
		for _, row := range []domain.Feedback{*f, counterpart} {
			if row.Published() {
				continue
			}
			if err := publishFeedback(ctx, tx, row.ID, now); err != nil {
				return err
			}
		}
		f.Publish(now)
		return nil
	})
}

func (r *Repo) FindFeedback(ctx context.Context, orderID int64, direction string) (domain.Feedback, error) {
	const q = `SELECT ` + feedbackColumns + ` FROM feedback
	           WHERE order_id = @order_id AND direction = @direction`
	return scanFeedback(r.pool.QueryRow(ctx, q, pgx.NamedArgs{
		"order_id": orderID, "direction": direction,
	}))
}

func (r *Repo) OrderFeedback(ctx context.Context, orderID int64) ([]domain.Feedback, error) {
	const q = `SELECT ` + feedbackColumns + ` FROM feedback WHERE order_id = @order_id`
	return r.queryFeedback(ctx, q, pgx.NamedArgs{"order_id": orderID})
}

// ListFeedback is the published feedback an account received. A blind row is not visible to
// anyone but its author, which is why the filter is a WHERE rather than a caller's job.
func (r *Repo) ListFeedback(ctx context.Context, f port.FeedbackFilter) ([]domain.Feedback, error) {
	const q = `SELECT ` + feedbackColumns + ` FROM feedback
	           WHERE ratee_id = @ratee_id AND published_at IS NOT NULL
	             AND (@direction::text IS NULL OR direction = @direction::feedback_direction)
	             AND (@before::timestamptz IS NULL OR created_at < @before::timestamptz)
	           ORDER BY created_at DESC, id DESC
	           LIMIT @limit`
	before, limit := cursorBound(f.Cursor)
	// The role is which side the ratee was on, which is the direction that rated them.
	direction := ""
	switch f.Role {
	case domain.RoleSeller:
		direction = domain.DirectionBuyerToSeller
	case domain.RoleBuyer:
		direction = domain.DirectionSellerToBuyer
	}
	args := pgx.NamedArgs{
		"ratee_id": f.RateeID, "direction": nullText(direction),
		"before": before, "limit": limit,
	}
	return r.queryFeedback(ctx, q, args)
}

// DueFeedback is the reveal list: blind rows whose window has run out. It reads the partial
// index on unpublished rows, so a large published history costs nothing.
func (r *Repo) DueFeedback(ctx context.Context, now time.Time, limit int) ([]domain.Feedback, error) {
	const q = `SELECT ` + feedbackColumns + ` FROM feedback
	           WHERE published_at IS NULL AND created_at + @window::interval < @now
	           ORDER BY created_at
	           LIMIT @limit`
	args := pgx.NamedArgs{
		"now": now, "limit": limit,
		"window": fmt.Sprintf("%d hours", int(domain.BlindWindow.Hours())),
	}
	return r.queryFeedback(ctx, q, args)
}

// PublishFeedback reveals a row and folds its rating into the ratee's reputation in one
// transaction, so a published rating is always a counted one.
func (r *Repo) PublishFeedback(ctx context.Context, id int64, at time.Time) error {
	return dbx.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		return publishFeedback(ctx, tx, id, at)
	})
}

// publishFeedback is the shared half: the guard is `published_at IS NULL`, so a second
// attempt counts nothing rather than counting twice.
func publishFeedback(ctx context.Context, tx pgx.Tx, id int64, at time.Time) error {
	const q = `UPDATE feedback SET published_at = @at
	           WHERE id = @id AND published_at IS NULL
	           RETURNING ratee_id, direction::text, rating`
	var rateeID int64
	var direction string
	var rating int16
	err := tx.QueryRow(ctx, q, pgx.NamedArgs{"id": id, "at": at}).
		Scan(&rateeID, &direction, &rating)
	if dbx.IsNoRows(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("db publish feedback: %w", err)
	}
	return addRating(ctx, tx, rateeID, domain.RoleRated(direction), int64(rating), 1)
}

// addRating folds a transaction rating into an account's reputation, creating the row when
// it is that account's first. The two rating pairs stay apart: this one is `rating_*`.
func addRating(ctx context.Context, tx pgx.Tx, accountID int64, role string, sum, count int64) error {
	const q = `INSERT INTO reputation (account_id, role, rating_sum, rating_count)
	           VALUES (@account_id, @role, @sum, @count)
	           ON CONFLICT (account_id, role) DO UPDATE
	             SET rating_sum = reputation.rating_sum + @sum,
	                 rating_count = reputation.rating_count + @count,
	                 updated_at = CURRENT_TIMESTAMP`
	args := pgx.NamedArgs{"account_id": accountID, "role": role, "sum": sum, "count": count}
	if _, err := tx.Exec(ctx, q, args); err != nil {
		return fmt.Errorf("db add rating: %w", err)
	}
	return nil
}

func (r *Repo) queryFeedback(ctx context.Context, q string, args pgx.NamedArgs) ([]domain.Feedback, error) {
	rows, err := r.pool.Query(ctx, q, args)
	if err != nil {
		return nil, fmt.Errorf("db query feedback: %w", err)
	}
	defer rows.Close()
	var out []domain.Feedback
	for rows.Next() {
		f, err := scanFeedback(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate feedback: %w", err)
	}
	return out, nil
}
