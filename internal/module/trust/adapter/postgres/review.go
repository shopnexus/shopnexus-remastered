package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/common/dbx"
	"shopnexus/internal/module/trust/domain"
	"shopnexus/internal/module/trust/port"
)

const reviewColumns = `id, listing_id, order_id, author_id, rating, body, attachments,
	       helpful_count, not_helpful_count, reply_count, created_at, updated_at`

func scanReview(row pgx.Row) (domain.Review, error) {
	var v domain.Review
	err := row.Scan(&v.ID, &v.ListingID, &v.OrderID, &v.AuthorID, &v.Rating, &v.Body,
		&v.Attachments, &v.HelpfulCount, &v.NotHelpfulCount, &v.ReplyCount,
		&v.CreatedAt, &v.UpdatedAt)
	if dbx.IsNoRows(err) {
		return domain.Review{}, domain.ErrReviewNotFound
	}
	if err != nil {
		return domain.Review{}, fmt.Errorf("db scan review: %w", err)
	}
	return v, nil
}

// InsertReview writes the review and folds its rating into the seller's reputation in one
// transaction: a review that counts towards a rating only after a second write is a rating
// that can be wrong for as long as that write is missing.
func (r *Repo) InsertReview(ctx context.Context, v *domain.Review, sellerID int64) error {
	return dbx.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		const q = `INSERT INTO review (listing_id, order_id, author_id, rating, body, attachments)
		           VALUES (@listing_id, @order_id, @author_id, @rating, @body, @attachments)
		           RETURNING id, created_at`
		args := pgx.NamedArgs{
			"listing_id": v.ListingID, "order_id": v.OrderID, "author_id": v.AuthorID,
			"rating": v.Rating, "body": v.Body,
			"attachments": dbx.Int64Array(v.Attachments),
		}
		if err := tx.QueryRow(ctx, q, args).Scan(&v.ID, &v.CreatedAt); err != nil {
			if dbx.IsUniqueViolation(err) {
				return domain.ErrReviewExists
			}
			return fmt.Errorf("db insert review: %w", err)
		}
		return addReviewRating(ctx, tx, sellerID, int64(v.Rating), 1)
	})
}

func (r *Repo) FindReview(ctx context.Context, id int64) (domain.Review, error) {
	const q = `SELECT ` + reviewColumns + ` FROM review WHERE id = @id`
	return scanReview(r.pool.QueryRow(ctx, q, pgx.NamedArgs{"id": id}))
}

// ListReviews is a listing's review page, or one author's reviews. The sort is a whitelist
// switch rather than a parameter: an ordering a client invents never reaches the SQL.
//
// `helpful` pages a counter other people are changing, so a review that gains votes can
// cross a page boundary. That drift is accepted — seeing a review twice costs the reader
// nothing — and `newest` is the key that does not move.
func (r *Repo) ListReviews(ctx context.Context, f port.ReviewFilter) ([]domain.Review, error) {
	const base = `SELECT ` + reviewColumns + ` FROM review
	           WHERE (@listing_id = 0 OR listing_id = @listing_id)
	             AND (@author_id = 0 OR author_id = @author_id)
	             AND (@rating = 0 OR rating = @rating)
	             AND (@before::timestamptz IS NULL OR created_at < @before::timestamptz)`
	q := base + ` ORDER BY created_at DESC, id DESC LIMIT @limit`
	if f.Sort == domain.ReviewSortHelpful {
		q = base + ` ORDER BY helpful_count DESC, id DESC LIMIT @limit`
	}
	before, limit := cursorBound(f.Cursor)
	args := pgx.NamedArgs{
		"listing_id": f.ListingID, "author_id": f.AuthorID, "rating": f.Rating,
		"before": before, "limit": limit,
	}
	return r.queryReviews(ctx, q, args)
}

// SaveReview writes an edit and moves the seller's review rating by the delta in the same
// transaction. A rating changed from 5 to 1 that leaves the aggregate alone is a number
// nobody can reproduce.
func (r *Repo) SaveReview(ctx context.Context, v domain.Review, sellerID int64, ratingDelta int64) error {
	return dbx.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		const q = `UPDATE review
		           SET rating = @rating, body = @body, attachments = @attachments,
		               updated_at = @updated_at
		           WHERE id = @id`
		args := pgx.NamedArgs{
			"id": v.ID, "rating": v.Rating, "body": v.Body,
			"attachments": dbx.Int64Array(v.Attachments), "updated_at": v.UpdatedAt,
		}
		tag, err := tx.Exec(ctx, q, args)
		if err != nil {
			return fmt.Errorf("db update review: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrReviewNotFound
		}
		if ratingDelta == 0 {
			return nil
		}
		return addReviewRating(ctx, tx, sellerID, ratingDelta, 0)
	})
}

// DeleteReview drops the review — its replies and votes go with it by cascade — and takes
// its rating back out of the seller's reputation.
func (r *Repo) DeleteReview(ctx context.Context, id int64, sellerID int64, rating int16) error {
	return dbx.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM review WHERE id = @id`, pgx.NamedArgs{"id": id})
		if err != nil {
			return fmt.Errorf("db delete review: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrReviewNotFound
		}
		return addReviewRating(ctx, tx, sellerID, -int64(rating), -1)
	})
}

func (r *Repo) queryReviews(ctx context.Context, q string, args pgx.NamedArgs) ([]domain.Review, error) {
	rows, err := r.pool.Query(ctx, q, args)
	if err != nil {
		return nil, fmt.Errorf("db query reviews: %w", err)
	}
	defer rows.Close()
	var out []domain.Review
	for rows.Next() {
		v, err := scanReview(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate reviews: %w", err)
	}
	return out, nil
}

// ---------------------------------------------------------------- replies ---

const replyColumns = `id, review_id, author_id, body, created_at`

func scanReply(row pgx.Row) (domain.ReviewReply, error) {
	var v domain.ReviewReply
	err := row.Scan(&v.ID, &v.ReviewID, &v.AuthorID, &v.Body, &v.CreatedAt)
	if dbx.IsNoRows(err) {
		return domain.ReviewReply{}, domain.ErrReplyNotFound
	}
	if err != nil {
		return domain.ReviewReply{}, fmt.Errorf("db scan reply: %w", err)
	}
	return v, nil
}

// InsertReply writes the reply and bumps the review's reply_count in one transaction, so the
// number a page renders is never ahead of or behind the thread it counts.
func (r *Repo) InsertReply(ctx context.Context, v *domain.ReviewReply) error {
	return dbx.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		const q = `INSERT INTO review_reply (review_id, author_id, body)
		           VALUES (@review_id, @author_id, @body)
		           RETURNING id, created_at`
		args := pgx.NamedArgs{"review_id": v.ReviewID, "author_id": v.AuthorID, "body": v.Body}
		if err := tx.QueryRow(ctx, q, args).Scan(&v.ID, &v.CreatedAt); err != nil {
			if dbx.IsForeignKeyViolation(err) {
				return domain.ErrReviewNotFound
			}
			return fmt.Errorf("db insert reply: %w", err)
		}
		return bumpReplyCount(ctx, tx, v.ReviewID, 1)
	})
}

func (r *Repo) FindReply(ctx context.Context, id int64) (domain.ReviewReply, error) {
	const q = `SELECT ` + replyColumns + ` FROM review_reply WHERE id = @id`
	return scanReply(r.pool.QueryRow(ctx, q, pgx.NamedArgs{"id": id}))
}

// ListReplies is the thread of each review in one query rather than one per row — that is
// what a page of twenty reviews needs. limit = 0 is the whole thread, which only the single
// review read asks for.
func (r *Repo) ListReplies(ctx context.Context, reviewIDs []int64, limit int) (map[int64][]domain.ReviewReply, error) {
	out := make(map[int64][]domain.ReviewReply, len(reviewIDs))
	if len(reviewIDs) == 0 {
		return out, nil
	}
	// The rank is per review, so a cap applies to each thread rather than to the page.
	const q = `SELECT ` + replyColumns + ` FROM (
	             SELECT id, review_id, author_id, body, created_at,
	                    row_number() OVER (PARTITION BY review_id ORDER BY created_at, id) AS rank
	             FROM review_reply WHERE review_id = ANY(@ids)
	           ) ranked
	           WHERE @limit = 0 OR rank <= @limit
	           ORDER BY review_id, created_at, id`
	rows, err := r.pool.Query(ctx, q, pgx.NamedArgs{"ids": reviewIDs, "limit": limit})
	if err != nil {
		return nil, fmt.Errorf("db query replies: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		v, err := scanReply(rows)
		if err != nil {
			return nil, err
		}
		out[v.ReviewID] = append(out[v.ReviewID], v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate replies: %w", err)
	}
	return out, nil
}

func (r *Repo) DeleteReply(ctx context.Context, id int64) error {
	return dbx.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		const q = `DELETE FROM review_reply WHERE id = @id RETURNING review_id`
		var reviewID int64
		err := tx.QueryRow(ctx, q, pgx.NamedArgs{"id": id}).Scan(&reviewID)
		if dbx.IsNoRows(err) {
			return domain.ErrReplyNotFound
		}
		if err != nil {
			return fmt.Errorf("db delete reply: %w", err)
		}
		return bumpReplyCount(ctx, tx, reviewID, -1)
	})
}

// bumpReplyCount keeps the denormalized count in step. GREATEST(0, …) is not used: a count
// that would go negative means this code is wrong, and the CHECK failing is how that is
// found rather than hidden.
func bumpReplyCount(ctx context.Context, tx pgx.Tx, reviewID int64, delta int64) error {
	const q = `UPDATE review SET reply_count = reply_count + @delta WHERE id = @id`
	if _, err := tx.Exec(ctx, q, pgx.NamedArgs{"id": reviewID, "delta": delta}); err != nil {
		return fmt.Errorf("db bump reply count: %w", err)
	}
	return nil
}

// ------------------------------------------------------------------ votes ---

// PutVote records or replaces a vote and moves both counters in the same transaction. The
// previous vote is read under the review's row lock, so two concurrent flips cannot each
// compute their delta from the same starting point.
func (r *Repo) PutVote(ctx context.Context, v domain.ReviewVote) (port.VoteTally, error) {
	var tally port.VoteTally
	err := dbx.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		previous, err := lockAndReadVote(ctx, tx, v.ReviewID, v.AccountID)
		if err != nil {
			return err
		}
		if previous == v.Vote {
			// Nothing to move: the caller is asking for the state the row is in.
			tally, err = readTally(ctx, tx, v.ReviewID, previous)
			return err
		}
		const q = `INSERT INTO review_vote (review_id, account_id, vote)
		           VALUES (@review_id, @account_id, @vote)
		           ON CONFLICT (review_id, account_id) DO UPDATE
		             SET vote = @vote, created_at = CURRENT_TIMESTAMP`
		args := pgx.NamedArgs{"review_id": v.ReviewID, "account_id": v.AccountID, "vote": v.Vote}
		if _, err := tx.Exec(ctx, q, args); err != nil {
			return fmt.Errorf("db put vote: %w", err)
		}
		helpful, notHelpful := domain.VoteDelta(previous, v.Vote)
		if err := moveTally(ctx, tx, v.ReviewID, helpful, notHelpful); err != nil {
			return err
		}
		tally, err = readTally(ctx, tx, v.ReviewID, v.Vote)
		return err
	})
	if err != nil {
		return port.VoteTally{}, err
	}
	return tally, nil
}

// DeleteVote withdraws a vote by removing the row: a stored zero would be a row that says
// nothing and would have to be excluded from every tally.
func (r *Repo) DeleteVote(ctx context.Context, reviewID, accountID int64) (port.VoteTally, error) {
	var tally port.VoteTally
	err := dbx.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		previous, err := lockAndReadVote(ctx, tx, reviewID, accountID)
		if err != nil {
			return err
		}
		if previous == 0 {
			return domain.ErrVoteNotFound
		}
		const q = `DELETE FROM review_vote WHERE review_id = @review_id AND account_id = @account_id`
		args := pgx.NamedArgs{"review_id": reviewID, "account_id": accountID}
		if _, err := tx.Exec(ctx, q, args); err != nil {
			return fmt.Errorf("db delete vote: %w", err)
		}
		helpful, notHelpful := domain.VoteDelta(previous, 0)
		if err := moveTally(ctx, tx, reviewID, helpful, notHelpful); err != nil {
			return err
		}
		tally, err = readTally(ctx, tx, reviewID, 0)
		return err
	})
	if err != nil {
		return port.VoteTally{}, err
	}
	return tally, nil
}

// lockAndReadVote takes the review's row lock and answers the caller's current vote, 0 for
// none. The lock is what serialises two flips on the same review: without it both would
// compute a delta from the same tally and one would be lost.
func lockAndReadVote(ctx context.Context, tx pgx.Tx, reviewID, accountID int64) (int16, error) {
	var exists int64
	err := tx.QueryRow(ctx, `SELECT id FROM review WHERE id = @id FOR UPDATE`,
		pgx.NamedArgs{"id": reviewID}).Scan(&exists)
	if dbx.IsNoRows(err) {
		return 0, domain.ErrReviewNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("db lock review: %w", err)
	}
	var vote int16
	err = tx.QueryRow(ctx, `SELECT vote FROM review_vote
	                        WHERE review_id = @review_id AND account_id = @account_id`,
		pgx.NamedArgs{"review_id": reviewID, "account_id": accountID}).Scan(&vote)
	if dbx.IsNoRows(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("db scan vote: %w", err)
	}
	return vote, nil
}

func moveTally(ctx context.Context, tx pgx.Tx, reviewID int64, helpful, notHelpful int64) error {
	const q = `UPDATE review
	           SET helpful_count = helpful_count + @helpful,
	               not_helpful_count = not_helpful_count + @not_helpful
	           WHERE id = @id`
	args := pgx.NamedArgs{"id": reviewID, "helpful": helpful, "not_helpful": notHelpful}
	if _, err := tx.Exec(ctx, q, args); err != nil {
		return fmt.Errorf("db move vote tally: %w", err)
	}
	return nil
}

func readTally(ctx context.Context, tx pgx.Tx, reviewID int64, myVote int16) (port.VoteTally, error) {
	const q = `SELECT helpful_count, not_helpful_count FROM review WHERE id = @id`
	var tally port.VoteTally
	err := tx.QueryRow(ctx, q, pgx.NamedArgs{"id": reviewID}).
		Scan(&tally.Helpful, &tally.NotHelpful)
	if err != nil {
		return port.VoteTally{}, fmt.Errorf("db scan vote tally: %w", err)
	}
	if myVote != 0 {
		tally.MyVote = &myVote
	}
	return tally, nil
}

// MyVotes is one account's votes across a page of reviews, so rendering a page costs one
// query rather than one per row.
func (r *Repo) MyVotes(ctx context.Context, accountID int64, reviewIDs []int64) (map[int64]int16, error) {
	out := make(map[int64]int16, len(reviewIDs))
	if accountID == 0 || len(reviewIDs) == 0 {
		return out, nil
	}
	const q = `SELECT review_id, vote FROM review_vote
	           WHERE account_id = @account_id AND review_id = ANY(@ids)`
	rows, err := r.pool.Query(ctx, q, pgx.NamedArgs{"account_id": accountID, "ids": reviewIDs})
	if err != nil {
		return nil, fmt.Errorf("db query my votes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var reviewID int64
		var vote int16
		if err := rows.Scan(&reviewID, &vote); err != nil {
			return nil, fmt.Errorf("db scan my vote: %w", err)
		}
		out[reviewID] = vote
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate my votes: %w", err)
	}
	return out, nil
}
