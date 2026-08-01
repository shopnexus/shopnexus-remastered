// Package trust is this marketplace's trust & safety module: blind two-way transaction feedback,
// product reviews, per-account reputation and the abuse-report queue. Publishing a rating folds it
// into reputation in the same transaction, and a settled order's counters arrive on the bus
// (see fx.go).
package trust

import (
	"embed"
	"io/fs"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrations returns the module's embedded SQL migrations.
func Migrations() fs.FS {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		panic(err)
	}
	return sub
}
