package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/common/dbx"
	"shopnexus/internal/module/order/domain"
)

const cartColumns = `id, account_id, listing_id, variant_id, quantity, created_at`

func scanCartItem(row pgx.Row) (domain.CartItem, error) {
	var c domain.CartItem
	err := row.Scan(&c.ID, &c.AccountID, &c.ListingID, &c.VariantID, &c.Quantity, &c.CreatedAt)
	if dbx.IsNoRows(err) {
		return domain.CartItem{}, domain.ErrCartItemNotFound
	}
	if err != nil {
		return domain.CartItem{}, fmt.Errorf("db scan cart item: %w", err)
	}
	return c, nil
}

// UpsertCartItem tops up rather than stacking: the row is keyed by (account, variant), so
// adding the same variant twice is one row with a larger quantity — which is what a cart
// means, and what the unique constraint already says.
func (r *Repo) UpsertCartItem(ctx context.Context, c *domain.CartItem) error {
	const q = `INSERT INTO cart_item (account_id, listing_id, variant_id, quantity)
	           VALUES (@account_id, @listing_id, @variant_id, @quantity)
	           ON CONFLICT (account_id, variant_id) DO UPDATE
	             SET quantity = cart_item.quantity + @quantity
	           RETURNING id, quantity, created_at`
	args := pgx.NamedArgs{
		"account_id": c.AccountID, "listing_id": c.ListingID,
		"variant_id": c.VariantID, "quantity": c.Quantity,
	}
	if err := r.pool.QueryRow(ctx, q, args).Scan(&c.ID, &c.Quantity, &c.CreatedAt); err != nil {
		return fmt.Errorf("db upsert cart item: %w", err)
	}
	return nil
}

func (r *Repo) FindCartItem(ctx context.Context, id, accountID int64) (domain.CartItem, error) {
	const q = `SELECT ` + cartColumns + ` FROM cart_item
	           WHERE id = @id AND account_id = @account_id`
	args := pgx.NamedArgs{"id": id, "account_id": accountID}
	return scanCartItem(r.pool.QueryRow(ctx, q, args))
}

func (r *Repo) ListCartItems(ctx context.Context, accountID int64) ([]domain.CartItem, error) {
	const q = `SELECT ` + cartColumns + ` FROM cart_item
	           WHERE account_id = @account_id ORDER BY created_at DESC, id DESC`
	rows, err := r.pool.Query(ctx, q, pgx.NamedArgs{"account_id": accountID})
	if err != nil {
		return nil, fmt.Errorf("db query cart items: %w", err)
	}
	defer rows.Close()

	var out []domain.CartItem
	for rows.Next() {
		c, err := scanCartItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate cart items: %w", err)
	}
	return out, nil
}

func (r *Repo) SaveCartItem(ctx context.Context, c domain.CartItem) error {
	const q = `UPDATE cart_item SET quantity = @quantity
	           WHERE id = @id AND account_id = @account_id`
	args := pgx.NamedArgs{"id": c.ID, "account_id": c.AccountID, "quantity": c.Quantity}
	tag, err := r.pool.Exec(ctx, q, args)
	if err != nil {
		return fmt.Errorf("db update cart item: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrCartItemNotFound
	}
	return nil
}

// DeleteCartItem is a real delete: a cart row records an intention, not a fact, so there is
// no history to preserve.
func (r *Repo) DeleteCartItem(ctx context.Context, id, accountID int64) error {
	const q = `DELETE FROM cart_item WHERE id = @id AND account_id = @account_id`
	args := pgx.NamedArgs{"id": id, "account_id": accountID}
	tag, err := r.pool.Exec(ctx, q, args)
	if err != nil {
		return fmt.Errorf("db delete cart item: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrCartItemNotFound
	}
	return nil
}

func (r *Repo) DeleteCartItemsByVariants(ctx context.Context, accountID int64, variantIDs []int64) error {
	if len(variantIDs) == 0 {
		return nil
	}
	const q = `DELETE FROM cart_item WHERE account_id = @account_id AND variant_id = ANY(@variant_ids)`
	args := pgx.NamedArgs{"account_id": accountID, "variant_ids": variantIDs}
	if _, err := r.pool.Exec(ctx, q, args); err != nil {
		return fmt.Errorf("db delete cart items by variants: %w", err)
	}
	return nil
}
