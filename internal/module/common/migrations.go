// Package common holds cross-module shared tables: uploaded file/media
// resources, resource-to-entity references, and the pluggable service option
// registry (payment/transport providers). Data-only for now — no service.
package common

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
