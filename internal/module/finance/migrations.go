// Package finance owns all money primitives in one module/DB so escrow moves
// stay atomic: payment sessions + append-only transaction ledger, per-account
// wallet (available/held) + wallet ledger, bank accounts, withdrawals, and
// tax info (seller_tax_info). order and account reference these by id only.
package finance

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
