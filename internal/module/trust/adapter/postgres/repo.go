// Package postgres implements the trust port.Repository with pgx named args and
// hand-written SQL.
//
// All SQL is unqualified: the pool sets search_path to this module's schema, so a table
// name is enough and the module can later move to its own database without a rewrite.
package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopnexus/internal/module/common"
	"shopnexus/internal/module/common/dbx"
	"shopnexus/internal/module/trust/port"
)

type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

var _ port.Repository = (*Repo)(nil)

// FindResources reads this module's own uploads — a review's photos. Shared DDL, per-schema
// rows: the upload belongs to trust.
func (r *Repo) FindResources(ctx context.Context, ids []int64) ([]common.Resource, error) {
	return dbx.NewResources(r.pool).Find(ctx, ids)
}

// addCursor fills in the bound every list here shares: the key the previous page ended at and
// that row's id, compared as a tuple. `@before_id = 0` is how a first page says "no bound",
// which is why no statement needs a second version of itself.
func addCursor(args pgx.NamedArgs, f port.CursorFilter) {
	args["before"] = dbx.NullTime(f.Before)
	args["before_count"] = f.BeforeCount
	args["before_id"] = f.BeforeID
	args["limit"] = f.Limit
}

// nullText is an optional filter: an empty string means "no filter", not "match the empty
// string".
func nullText(s string) any {
	if s == "" {
		return nil
	}
	return s
}
