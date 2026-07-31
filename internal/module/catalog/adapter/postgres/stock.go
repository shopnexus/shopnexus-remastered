package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/module/common/dbx"
)

func (r *Repo) FindStock(ctx context.Context, variantID int64) (domain.Stock, error) {
	const q = `SELECT quantity, reserved, sold FROM stock WHERE variant_id = @variant_id`
	var s domain.Stock
	err := r.pool.QueryRow(ctx, q, pgx.NamedArgs{"variant_id": variantID}).
		Scan(&s.Quantity, &s.Reserved, &s.Sold)
	if dbx.IsNoRows(err) {
		return domain.Stock{}, domain.ErrVariantNotFound
	}
	if err != nil {
		return domain.Stock{}, fmt.Errorf("db scan stock: %w", err)
	}
	return s, nil
}

// ReserveStock holds units for a checkout. The WHERE clause is the invariant: there is no
// read, so nothing can change between deciding and writing, and the CHECK constraint is a
// second line rather than the first.
func (r *Repo) ReserveStock(ctx context.Context, variantID, units int64) error {
	const q = `UPDATE stock SET reserved = reserved + @units
	           WHERE variant_id = @variant_id AND reserved + sold + @units <= quantity`
	return r.moveStock(ctx, q, variantID, units)
}

// ReleaseStock gives units back when a session is cancelled or expires.
func (r *Repo) ReleaseStock(ctx context.Context, variantID, units int64) error {
	const q = `UPDATE stock SET reserved = reserved - @units
	           WHERE variant_id = @variant_id AND reserved >= @units`
	return r.moveStock(ctx, q, variantID, units)
}

// CommitStock turns a reservation into a sale. It also bumps the listing's counter, in the
// same transaction: cached_sold is a cache, but one that drifts is worse than one that is
// recomputed, and the two writes have no reason to be apart.
func (r *Repo) CommitStock(ctx context.Context, variantID, units int64) error {
	if units <= 0 {
		return domain.ErrInsufficientStock
	}
	return dbx.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		const q = `UPDATE stock SET reserved = reserved - @units, sold = sold + @units
		           WHERE variant_id = @variant_id AND reserved >= @units`
		tag, err := tx.Exec(ctx, q, pgx.NamedArgs{"variant_id": variantID, "units": units})
		if err != nil {
			return fmt.Errorf("db commit stock: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrInsufficientStock
		}
		const counter = `UPDATE listing SET cached_sold = cached_sold + @units
		                 WHERE id = (SELECT listing_id FROM variant WHERE id = @variant_id)`
		if _, err := tx.Exec(ctx, counter, pgx.NamedArgs{"variant_id": variantID, "units": units}); err != nil {
			return fmt.Errorf("db bump cached sold: %w", err)
		}
		return nil
	})
}

// moveStock runs one guarded statement. Zero rows is the refusal — a variant that does not
// exist and one with no room are the same answer to a caller, and telling them apart costs a
// second query on the hot path to change nothing.
func (r *Repo) moveStock(ctx context.Context, sql string, variantID, units int64) error {
	if units <= 0 {
		return domain.ErrInsufficientStock
	}
	tag, err := r.pool.Exec(ctx, sql, pgx.NamedArgs{"variant_id": variantID, "units": units})
	if err != nil {
		return fmt.Errorf("db move stock: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrInsufficientStock
	}
	return nil
}

// SetCachedRating writes the average trust recomputed and the count behind it. A listing that
// no longer exists is not an error: its reviews outlive it, so there is nothing left to
// cache and nothing wrong with the caller.
func (r *Repo) SetCachedRating(ctx context.Context, listingID int64, rating float64, count int64) error {
	const q = `UPDATE listing SET cached_rating = @rating, cached_review_count = @count
	           WHERE id = @id`
	args := pgx.NamedArgs{"id": listingID, "rating": rating, "count": count}
	if _, err := r.pool.Exec(ctx, q, args); err != nil {
		return fmt.Errorf("db set cached rating: %w", err)
	}
	return nil
}
