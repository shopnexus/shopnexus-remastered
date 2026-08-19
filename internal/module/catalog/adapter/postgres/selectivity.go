package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/module/catalog/port"
	"shopnexus/internal/module/common/dbx"
)

// activeListing is the row set every count here is over: what a shopper can actually be shown.
// A draft or a taken-down listing is not part of the catalogue a signal narrows.
const activeListing = ` WHERE l.deleted_at IS NULL AND l.status = 'active'`

// SignalSelectivity reads the whole table — a few dozen rows — and derives the active-listing
// total from it rather than counting `listing` again on the search path.
//
// The total is the sum of the condition rows: `listing.condition` is NOT NULL, so every active
// listing is counted under exactly one condition label and that sum *is* the total. A count
// over the table would be a scan per search for a number this pass already computed.
func (r *Repo) SignalSelectivity(ctx context.Context) (domain.Selectivity, error) {
	const q = `SELECT kind, key, listings FROM signal_selectivity`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return domain.Selectivity{}, fmt.Errorf("db query signal selectivity: %w", err)
	}
	defer rows.Close()

	out := domain.Selectivity{Counts: make(map[domain.SelectivityKey]int64)}
	for rows.Next() {
		var (
			key      domain.SelectivityKey
			listings int64
		)
		if err := rows.Scan(&key.Kind, &key.Key, &listings); err != nil {
			return domain.Selectivity{}, fmt.Errorf("db scan signal selectivity: %w", err)
		}
		out.Counts[key] = listings
		if key.Kind == port.PredicateCondition {
			out.Total += listings
		}
	}
	if err := rows.Err(); err != nil {
		return domain.Selectivity{}, fmt.Errorf("db iterate signal selectivity: %w", err)
	}
	return out, nil
}

// selectivityCounts recounts all three kinds. The kind literals are concatenated from the port
// constants, so renaming one is a build failure rather than a table of counts nothing looks up.
//
// The category count is of that category alone, not its subtree, because `PredicateCategory` is
// an equality on `category_id`: a count that included descendants would scale the weight by a
// share the predicate never matches.
const selectivityCounts = `
	           SELECT '` + port.PredicateCategory + `', l.category_id::text, count(*)
	           FROM listing l` + activeListing + `
	           GROUP BY l.category_id

	           UNION ALL

	           SELECT '` + port.PredicateCondition + `', l.condition::text, count(*)
	           FROM listing l` + activeListing + `
	           GROUP BY l.condition

	           UNION ALL

	           SELECT '` + port.PredicateTag + `', lt.tag, count(*)
	           FROM listing_tag lt
	           JOIN listing l ON l.id = lt.listing_id` + activeListing + `
	           GROUP BY lt.tag`

// RefreshSignalSelectivity replaces the whole table in one transaction.
//
// Delete then insert rather than an upsert plus a reconciling delete: the recount produces the
// complete set, so a key whose last listing was deleted has to disappear, and one statement that
// cannot leave a stale row behind beats two that have to agree about which rows those are. A
// reader in another transaction sees either the old set or the new one.
func (r *Repo) RefreshSignalSelectivity(ctx context.Context) error {
	const clear = `DELETE FROM signal_selectivity`
	const insert = `INSERT INTO signal_selectivity (kind, key, listings, updated_at)
	           SELECT kind, key, listings, now() FROM (` + selectivityCounts + `) c(kind, key, listings)`
	err := dbx.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, clear); err != nil {
			return fmt.Errorf("db clear signal selectivity: %w", err)
		}
		if _, err := tx.Exec(ctx, insert); err != nil {
			return fmt.Errorf("db insert signal selectivity: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("refresh signal selectivity: %w", err)
	}
	return nil
}
