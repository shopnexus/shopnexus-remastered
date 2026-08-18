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
