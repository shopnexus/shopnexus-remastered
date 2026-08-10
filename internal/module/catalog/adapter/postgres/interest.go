package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/module/catalog/port"
	"shopnexus/internal/module/common/dbx"
)

// Interests reads the account's slots. A handful of rows by primary key: the nearest-neighbour
// search runs against listing_embedding, not here, which is why the table carries no vector
// index of its own.
func (r *Repo) Interests(ctx context.Context, accountID int64) ([]port.Interest, error) {
	const q = `SELECT dense::text, strength FROM account_interest
	           WHERE account_id = @account_id
	           ORDER BY strength DESC, slot`
	rows, err := r.pool.Query(ctx, q, pgx.NamedArgs{"account_id": accountID})
	if err != nil {
		return nil, fmt.Errorf("db query interests: %w", err)
	}
	defer rows.Close()

	var out []port.Interest
	for rows.Next() {
		var (
			literal  string
			strength float64
		)
		if err := rows.Scan(&literal, &strength); err != nil {
			return nil, fmt.Errorf("db scan interest: %w", err)
		}
		v, err := parseVector(literal)
		if err != nil {
			return nil, err
		}
		out = append(out, port.Interest{Vector: v, Weight: strength})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate interests: %w", err)
	}
	return out, nil
}

// interestSignals is what an account has told us they are into: the listings on their
// wishlist, each carrying the vector of the listing and how much of a say it still has.
//
// Category is what groups them, rather than a clustering pass over the vectors. The tree is
// already the taxonomy a seller filed the goods under and a buyer browsed them by, so it
// separates phones from bicycles at no cost, is stable between recomputes, and is
// explainable — where k-means over four hundred saved vectors would move an account's
// interests around on every run and answer nobody's question about why.
const interestSignals = `
	           WITH signal AS (
	             SELECT l.category_id, e.dense,
	                    -- Halving decay, its exponent capped: an account whose wishlist is
	                    -- years old still has a taste, and exp() underflows rather than
	                    -- rounding to zero.
	                    exp(-ln(2) * least(
	                          extract(epoch FROM now() - f.created_at) / @half_life_seconds,
	                          50)) AS weight
	             FROM favorite f
	             JOIN listing l ON l.id = f.listing_id AND l.deleted_at IS NULL
	             JOIN listing_embedding e ON e.listing_id = l.id AND e.dense IS NOT NULL
	             WHERE f.account_id = @account_id
	             ORDER BY f.created_at DESC
	             LIMIT @signals
	           ),
	           grouped AS (
	             SELECT category_id, sum(weight) AS strength, avg(dense) AS dense
	             FROM signal GROUP BY category_id
	           ),
	           top AS (
	             SELECT dense, strength,
	                    row_number() OVER (ORDER BY strength DESC, category_id) AS slot
	             FROM grouped
	             ORDER BY strength DESC, category_id
	             LIMIT @slots
	           )`

// RecomputeInterests rebuilds one account's slots from its wishlist.
//
// Delete then insert, in a transaction, rather than an upsert: an account that dropped from
// three interests to two must lose the third, and a reader that caught the pair half-written
// would rank a feed against somebody's taste plus a leftover. The centroids are averaged in
// the database because that is where both the vectors and the wishlist already are — moving
// four hundred embeddings into Go to average them would be the whole cost of the operation.
func (r *Repo) RecomputeInterests(ctx context.Context, accountID int64) error {
	// Strength is stored as a share of the whole signal, so a feed can hand each interest a
	// proportional slice of the page without knowing how the numbers were arrived at.
	const insert = interestSignals + `
	           INSERT INTO account_interest (account_id, slot, dense, strength, updated_at)
	           SELECT @account_id, slot, dense,
	                  strength / (SELECT sum(strength) FROM top), now()
	           FROM top`
	const clear = `DELETE FROM account_interest WHERE account_id = @account_id`

	args := pgx.NamedArgs{
		"account_id":        accountID,
		"half_life_seconds": domain.InterestHalfLife.Seconds(),
		"signals":           domain.InterestSignals,
		"slots":             domain.NumInterests,
	}
	err := dbx.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, clear, pgx.NamedArgs{"account_id": accountID}); err != nil {
			return fmt.Errorf("db clear interests: %w", err)
		}
		if _, err := tx.Exec(ctx, insert, args); err != nil {
			return fmt.Errorf("db insert interests: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("recompute interests: %w", err)
	}
	return nil
}

// StaleInterests names the accounts whose slots are behind their wishlist. Three ways that
// happens and one query for all of them: nothing computed yet, something saved since, or a
// saved listing whose vector was only written afterwards — the last being the ordinary case,
// since the embedding worker runs on its own clock and a listing saved the minute it was
// posted has no vector to average yet.
//
// Joined on slot 1 so an account appears once whatever it has, and unsaving is covered by the
// recompute the wishlist write already runs: a row that is gone leaves nothing to compare.
func (r *Repo) StaleInterests(ctx context.Context, limit int) ([]int64, error) {
	const q = `SELECT DISTINCT f.account_id
	           FROM favorite f
	           JOIN listing_embedding e ON e.listing_id = f.listing_id AND e.dense IS NOT NULL
	           LEFT JOIN account_interest i ON i.account_id = f.account_id AND i.slot = 1
	           WHERE i.updated_at IS NULL
	              OR f.created_at > i.updated_at
	              OR e.updated_at > i.updated_at
	           LIMIT @limit`
	rows, err := r.pool.Query(ctx, q, pgx.NamedArgs{"limit": limit})
	if err != nil {
		return nil, fmt.Errorf("db query stale interests: %w", err)
	}
	defer rows.Close()

	var out []int64
	for rows.Next() {
		var accountID int64
		if err := rows.Scan(&accountID); err != nil {
			return nil, fmt.Errorf("db scan stale interest: %w", err)
		}
		out = append(out, accountID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate stale interests: %w", err)
	}
	return out, nil
}
