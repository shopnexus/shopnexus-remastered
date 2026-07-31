package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/common/dbx"
	"shopnexus/internal/module/trust/domain"
)

const reputationColumns = `account_id, role::text, rating_sum, rating_count,
	       review_rating_sum, review_rating_count, completed_orders, cancelled_orders, updated_at`

// FindReputation answers a zero-valued aggregate rather than not-found: an account nobody
// has rated yet has a reputation of "none", and a 404 there would make every new seller's
// profile page an error.
func (r *Repo) FindReputation(ctx context.Context, accountID int64, role string) (domain.Reputation, error) {
	const q = `SELECT ` + reputationColumns + ` FROM reputation
	           WHERE account_id = @account_id AND role = @role`
	var rep domain.Reputation
	err := r.pool.QueryRow(ctx, q, pgx.NamedArgs{"account_id": accountID, "role": role}).
		Scan(&rep.AccountID, &rep.Role, &rep.RatingSum, &rep.RatingCount,
			&rep.ReviewRatingSum, &rep.ReviewRatingCount,
			&rep.CompletedOrders, &rep.CancelledOrders, &rep.UpdatedAt)
	if dbx.IsNoRows(err) {
		return domain.Reputation{AccountID: accountID, Role: role}, nil
	}
	if err != nil {
		return domain.Reputation{}, fmt.Errorf("db scan reputation: %w", err)
	}
	return rep, nil
}

// AddOrderOutcome bumps the completed or cancelled counter of both parties, each in the role
// they played. One statement per party rather than a join: they are two rows in the same
// table and the upsert has to create either.
func (r *Repo) AddOrderOutcome(ctx context.Context, buyerID, sellerID int64, completed bool) error {
	return dbx.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		for _, party := range []struct {
			id   int64
			role string
		}{{buyerID, domain.RoleBuyer}, {sellerID, domain.RoleSeller}} {
			if err := addOutcome(ctx, tx, party.id, party.role, completed); err != nil {
				return err
			}
		}
		return nil
	})
}

func addOutcome(ctx context.Context, tx pgx.Tx, accountID int64, role string, completed bool) error {
	const q = `INSERT INTO reputation (account_id, role, completed_orders, cancelled_orders)
	           VALUES (@account_id, @role, @completed, @cancelled)
	           ON CONFLICT (account_id, role) DO UPDATE
	             SET completed_orders = reputation.completed_orders + @completed,
	                 cancelled_orders = reputation.cancelled_orders + @cancelled,
	                 updated_at = CURRENT_TIMESTAMP`
	one, zero := int64(1), int64(0)
	args := pgx.NamedArgs{"account_id": accountID, "role": role, "completed": one, "cancelled": zero}
	if !completed {
		args["completed"], args["cancelled"] = zero, one
	}
	if _, err := tx.Exec(ctx, q, args); err != nil {
		return fmt.Errorf("db add order outcome: %w", err)
	}
	return nil
}

// ReviewAverage is a listing's rating over its reviews, which catalog caches. Both numbers,
// because an average with no count behind it cannot be rendered honestly.
func (r *Repo) ReviewAverage(ctx context.Context, listingID int64) (float64, int64, error) {
	const q = `SELECT COALESCE(AVG(rating), 0)::double precision, COUNT(*)
	           FROM review WHERE listing_id = @listing_id`
	var average float64
	var count int64
	err := r.pool.QueryRow(ctx, q, pgx.NamedArgs{"listing_id": listingID}).Scan(&average, &count)
	if err != nil {
		return 0, 0, fmt.Errorf("db scan review average: %w", err)
	}
	return average, count, nil
}

// addReviewRating folds a product rating into a seller's reputation. Its own pair of
// columns, never added to the transaction ratings: one order can produce both, and summing
// them would count that order twice.
//
// Update first, insert only if there was nothing to update — not an upsert. Postgres checks
// a constraint against the proposed row before it detects the conflict, so an ON CONFLICT
// carrying the negative delta of a deleted review fails "counters_non_negative" on a row it
// was never going to write. The insert keeps the raw values, so removing a rating that was
// never counted still fails loudly: that is this code being wrong, not a state to absorb.
func addReviewRating(ctx context.Context, tx pgx.Tx, sellerID, sum, count int64) error {
	const update = `UPDATE reputation
	                SET review_rating_sum = review_rating_sum + @sum,
	                    review_rating_count = review_rating_count + @count,
	                    updated_at = CURRENT_TIMESTAMP
	                WHERE account_id = @seller_id AND role = 'seller'`
	args := pgx.NamedArgs{"seller_id": sellerID, "sum": sum, "count": count}
	tag, err := tx.Exec(ctx, update, args)
	if err != nil {
		return fmt.Errorf("db add review rating: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	const insert = `INSERT INTO reputation (account_id, role, review_rating_sum, review_rating_count)
	                VALUES (@seller_id, 'seller', @sum, @count)`
	_, err = tx.Exec(ctx, insert, args)
	if dbx.IsUniqueViolation(err) {
		// Two first reviews of the same seller at once: the loser applies its delta to the
		// row the winner created rather than starting a second aggregate.
		if _, err := tx.Exec(ctx, update, args); err != nil {
			return fmt.Errorf("db add review rating: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("db add review rating: %w", err)
	}
	return nil
}
