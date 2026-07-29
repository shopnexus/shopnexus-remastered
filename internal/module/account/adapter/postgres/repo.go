// Package postgres implements the account port.Repository using pgx named args.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/module/account/port"
)

type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

var _ port.Repository = (*Repo)(nil)

func (r *Repo) Create(ctx context.Context, a *domain.Account) error {
	const q = `INSERT INTO accounts (email, password_hash, name)
	           VALUES (@email, @password_hash, @name)
	           RETURNING id, created_at`
	args := pgx.NamedArgs{"email": a.Email, "password_hash": a.PasswordHash, "name": a.Name}
	if err := r.pool.QueryRow(ctx, q, args).Scan(&a.ID, &a.CreatedAt); err != nil {
		return fmt.Errorf("db insert account: %w", err)
	}
	return nil
}

func (r *Repo) ExistsByEmail(ctx context.Context, email string) (bool, error) {
	const q = `SELECT EXISTS (SELECT 1 FROM accounts WHERE email = @email)`
	var exists bool
	if err := r.pool.QueryRow(ctx, q, pgx.NamedArgs{"email": email}).Scan(&exists); err != nil {
		return false, fmt.Errorf("db query account exists by email: %w", err)
	}
	return exists, nil
}

func (r *Repo) FindByEmail(ctx context.Context, email string) (domain.Account, error) {
	const q = `SELECT id, email, password_hash, name, created_at
	           FROM accounts WHERE email = @email`
	return r.queryOne(ctx, q, pgx.NamedArgs{"email": email})
}

func (r *Repo) FindByID(ctx context.Context, id int64) (domain.Account, error) {
	const q = `SELECT id, email, password_hash, name, created_at
	           FROM accounts WHERE id = @id`
	return r.queryOne(ctx, q, pgx.NamedArgs{"id": id})
}

func (r *Repo) queryOne(ctx context.Context, q string, args pgx.NamedArgs) (domain.Account, error) {
	var a domain.Account
	err := r.pool.QueryRow(ctx, q, args).Scan(&a.ID, &a.Email, &a.PasswordHash, &a.Name, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Account{}, domain.ErrAccountNotFound
	}
	if err != nil {
		return domain.Account{}, fmt.Errorf("db scan account: %w", err)
	}
	return a, nil
}
