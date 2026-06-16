// Package orderrepo is the pgx v5 data layer for the order module.
package orderrepo

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DBTX is satisfied by *pgxpool.Pool and pgx.Tx.
type DBTX interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type Repository struct {
	db DBTX
}

func New(db DBTX) *Repository { return &Repository{db: db} }

func (r *Repository) WithTx(tx pgx.Tx) *Repository { return &Repository{db: tx} }
