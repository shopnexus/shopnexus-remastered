// Package postgres implements the chat port.Repository with pgx named args and
// hand-written SQL.
//
// All SQL is unqualified: the pool sets search_path to this module's schema, so a table
// name is enough and the module can later move to its own database without a rewrite.
package postgres

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"shopnexus/internal/module/chat/port"
)

type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

var _ port.Repository = (*Repo)(nil)
