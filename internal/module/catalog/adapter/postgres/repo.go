// Package postgres implements the catalog port.Repository using pgx named args.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/module/catalog/port"
)

type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

var _ port.Repository = (*Repo)(nil)

func (r *Repo) Save(ctx context.Context, l *domain.Listing) error {
	const q = `INSERT INTO listings (owner_id, title, price, status)
	           VALUES (@owner_id, @title, @price, @status)
	           RETURNING id, created_at`
	args := pgx.NamedArgs{"owner_id": l.OwnerID, "title": l.Title, "price": l.Price, "status": l.Status}
	if err := r.pool.QueryRow(ctx, q, args).Scan(&l.ID, &l.CreatedAt); err != nil {
		return fmt.Errorf("db insert listing: %w", err)
	}
	return nil
}

func (r *Repo) FindByID(ctx context.Context, id int64) (domain.Listing, error) {
	const q = `SELECT id, owner_id, title, price, status, created_at
	           FROM listings WHERE id = @id`
	var l domain.Listing
	err := r.pool.QueryRow(ctx, q, pgx.NamedArgs{"id": id}).
		Scan(&l.ID, &l.OwnerID, &l.Title, &l.Price, &l.Status, &l.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Listing{}, domain.ErrListingNotFound
	}
	if err != nil {
		return domain.Listing{}, fmt.Errorf("db scan listing: %w", err)
	}
	return l, nil
}

func (r *Repo) List(ctx context.Context, limit, offset int) ([]domain.Listing, error) {
	const q = `SELECT id, owner_id, title, price, status, created_at
	           FROM listings ORDER BY created_at DESC
	           LIMIT @limit OFFSET @offset`
	rows, err := r.pool.Query(ctx, q, pgx.NamedArgs{"limit": limit, "offset": offset})
	if err != nil {
		return nil, fmt.Errorf("db query listings: %w", err)
	}
	defer rows.Close()

	var out []domain.Listing
	for rows.Next() {
		var l domain.Listing
		if err := rows.Scan(&l.ID, &l.OwnerID, &l.Title, &l.Price, &l.Status, &l.CreatedAt); err != nil {
			return nil, fmt.Errorf("db scan listing row: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate listings: %w", err)
	}
	return out, nil
}

// UpsertStock sets the stock quantity for a product (ref_type = 'product-sku').
// Uses INSERT … ON CONFLICT to create the row on first call.
func (r *Repo) UpsertStock(ctx context.Context, productID int64, quantity int64) error {
	const q = `INSERT INTO stock (ref_id, ref_type, stock, taken)
	           VALUES (@ref_id, 'product-sku', @quantity, 0)
	           ON CONFLICT (ref_id, ref_type) DO UPDATE SET stock = @quantity`
	_, err := r.pool.Exec(ctx, q, pgx.NamedArgs{"ref_id": productID, "quantity": quantity})
	if err != nil {
		return fmt.Errorf("db upsert stock: %w", err)
	}
	return nil
}

// FindStock returns the stock quantity for a product. Returns 0 (not an error) if no row exists.
func (r *Repo) FindStock(ctx context.Context, productID int64) (int64, error) {
	const q = `SELECT stock FROM stock WHERE ref_id = @ref_id AND ref_type = 'product-sku'`
	var qty int64
	err := r.pool.QueryRow(ctx, q, pgx.NamedArgs{"ref_id": productID}).Scan(&qty)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("db scan stock: %w", err)
	}
	return qty, nil
}
