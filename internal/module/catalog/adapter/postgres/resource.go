package postgres

import (
	"context"

	"shopnexus/internal/module/common"
	"shopnexus/internal/module/common/dbx"
)

// FindResources reads this module's own uploaded images — the listing gallery and the
// per-variant photos. Shared DDL, per-schema rows: the images belong to catalog.
func (r *Repo) FindResources(ctx context.Context, ids []int64) ([]common.Resource, error) {
	return dbx.NewResources(r.pool).Find(ctx, ids)
}
