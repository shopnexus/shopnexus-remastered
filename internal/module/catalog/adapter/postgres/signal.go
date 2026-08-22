package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/catalog/port"
	"shopnexus/internal/module/common/dbx"
)

// InsertListingSignals writes one row per action. Small batches — this module's own
// subscriber to its own bus, capped by the request that produced them (20) and by however
// many the subscriber's own linger window collected — so a transaction of individual inserts
// costs nothing worth a COPY for.
func (r *Repo) InsertListingSignals(ctx context.Context, signals []port.ListingSignal) error {
	if len(signals) == 0 {
		return nil
	}
	const q = `INSERT INTO listing_signal (account_id, listing_id, type)
	           VALUES (@account_id, @listing_id, @type)`
	return dbx.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		for _, s := range signals {
			args := pgx.NamedArgs{"account_id": s.AccountID, "listing_id": s.ListingID, "type": s.Type}
			if _, err := tx.Exec(ctx, q, args); err != nil {
				return fmt.Errorf("db insert listing signal: %w", err)
			}
		}
		return nil
	})
}

// RecentSignals is an account's last actions, most recent first — served by
// listing_signal_account_id_created_at_idx, the index interestSignals already reads on.
//
// One row per listing: a shopper who opened the same listing four times has one reason, not
// four, and a shelf drawn from the raw rows would be four shelves about one thing.
func (r *Repo) RecentSignals(ctx context.Context, accountID int64, types []string, limit int) ([]port.ListingSignal, error) {
	const q = `SELECT DISTINCT ON (listing_id) account_id, listing_id, type, created_at
	           FROM listing_signal
	           WHERE account_id = @account_id
	             AND (@types::text[] IS NULL OR type = ANY(@types::text[]))
	           ORDER BY listing_id, created_at DESC`
	// The de-duplication has to order by listing_id first, so the recency order is put back
	// outside it rather than asked of DISTINCT ON, which cannot answer both at once.
	const outer = `SELECT account_id, listing_id, type FROM (` + q + `) latest
	               ORDER BY created_at DESC LIMIT @limit`
	args := pgx.NamedArgs{
		"account_id": accountID,
		"types":      nullTextArray(types),
		"limit":      limit,
	}
	rows, err := r.pool.Query(ctx, outer, args)
	if err != nil {
		return nil, fmt.Errorf("db query recent signals: %w", err)
	}
	defer rows.Close()
	var out []port.ListingSignal
	for rows.Next() {
		var s port.ListingSignal
		if err := rows.Scan(&s.AccountID, &s.ListingID, &s.Type); err != nil {
			return nil, fmt.Errorf("db scan recent signal: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate recent signals: %w", err)
	}
	return out, nil
}
