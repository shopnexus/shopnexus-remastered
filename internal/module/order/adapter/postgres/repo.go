// Package postgres implements the order port.Repository with pgx named args and
// hand-written SQL.
//
// All SQL is unqualified: the pool sets search_path to this module's schema, so a table
// name is enough and the module can later move to its own database without a rewrite.
package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"shopnexus/internal/module/common"
	"shopnexus/internal/module/common/dbx"
	"shopnexus/internal/module/order/port"
)

type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

var _ port.Repository = (*Repo)(nil)

// FindResources reads this module's own uploaded evidence — receipt photos, refund
// attachments. Shared DDL, per-schema rows: the upload belongs to order.
func (r *Repo) FindResources(ctx context.Context, ids []int64) ([]common.Resource, error) {
	return dbx.NewResources(r.pool).Find(ctx, ids)
}

// cursorBound is the shared shape of every list here: bounded by the (created_at, id) pair
// the previous page ended at. Both, because a tuple comparison is the only bound that does not
// skip the rest of a group of rows sharing one transaction's timestamp.
func cursorBound(f port.CursorFilter) (before, beforeID any, limit int) {
	return dbx.NullTime(f.Before), dbx.NullID(f.BeforeID), f.Limit
}
