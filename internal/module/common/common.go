// Package common is what every module shares: the DDL for the tables they all have, the
// entities those tables carry, and — in common/dbx — the pgx helpers their adapters used to
// each redeclare.
//
// It is not a module and has no service. Nothing calls it over an interface: a module embeds
// what it needs. The shared DDL is applied into *every* module's schema, so a module owns its
// own audit trail, its own uploaded resources and the options of the kind it acts on, and can
// move to its own database with them. In dev every DSN points at the same server.
//
// This package imports only the standard library and shared/id, so a `port` or an `api`
// package can name these entities without pulling pgx behind them.
package common

import (
	"embed"
	"io/fs"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrations returns the shared DDL, applied into every module's schema before that module's
// own migrations. The numbering is dependency order — option references resource — and the
// runner tracks each file by name, so the shared set and a module's own set coexist in one
// schema_migrations table.
func Migrations() fs.FS {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		panic(err)
	}
	return sub
}
