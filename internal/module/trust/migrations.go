// Package trust is the C2C trust & safety schema: two-way transaction
// feedback, per-account reputation aggregates, and polymorphic abuse reports.
// Data-only for now — no service. Reputation is maintained by the (future)
// trust service subscribing to order.completed / order.cancelled on the bus.
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
