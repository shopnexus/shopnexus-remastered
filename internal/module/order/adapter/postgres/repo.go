// Package postgres implements the order port.Repository using pgx named args.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopnexus/internal/module/order/domain"
	"shopnexus/internal/module/order/port"
)

type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

var _ port.Repository = (*Repo)(nil)

func (r *Repo) Save(ctx context.Context, o *domain.Order) error {
	const q = `INSERT INTO orders (buyer_id, total, status)
	           VALUES (@buyer_id, @total, @status)
	           RETURNING id, created_at`
	args := pgx.NamedArgs{"buyer_id": o.BuyerID, "total": o.Total, "status": o.Status}
	if err := r.pool.QueryRow(ctx, q, args).Scan(&o.ID, &o.CreatedAt); err != nil {
		return fmt.Errorf("db insert order: %w", err)
	}
	return nil
}

func (r *Repo) FindByID(ctx context.Context, id int64) (domain.Order, error) {
	const q = `SELECT id, buyer_id, total, status, created_at
	           FROM orders WHERE id = @id`
	var o domain.Order
	err := r.pool.QueryRow(ctx, q, pgx.NamedArgs{"id": id}).
		Scan(&o.ID, &o.BuyerID, &o.Total, &o.Status, &o.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Order{}, domain.ErrOrderNotFound
	}
	if err != nil {
		return domain.Order{}, fmt.Errorf("db scan order: %w", err)
	}
	return o, nil
}
