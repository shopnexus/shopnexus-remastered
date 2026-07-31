package postgres

import (
	"context"

	"shopnexus/internal/module/common"
	"shopnexus/internal/module/common/dbx"
)

// FindResources reads this module's own uploaded resources — avatars and identity scans. The
// table is shared DDL applied into every schema, so the rows are the account module's and
// travel with it; the query itself lives once, in dbx.
func (r *Repo) FindResources(ctx context.Context, ids []int64) ([]common.Resource, error) {
	return dbx.NewResources(r.pool).Find(ctx, ids)
}
