// Package observability is cross-cutting operational telemetry: it records HTTP
// RED metrics, Go runtime samples, and mirrored bus events into the
// observability schema as TimescaleDB hypertables for Grafana to visualize.
// Product/web analytics lives outside the backend (Rybbit + ClickHouse).
package observability

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
