// Command migrate applies each module's embedded SQL migrations into its own
// schema (the module name) within its database. Run it as a CI/CD step or a
// Kubernetes Job/initContainer before rolling out the app — the application
// does not migrate at startup. Each module's DSN may point at the same URL
// (shared DB, isolated by schema) or a dedicated one once split out.
package main

import (
	"context"
	"io/fs"
	"log"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/config"
	"shopnexus/internal/infra/postgres"
	"shopnexus/internal/shared/validation"

	"shopnexus/internal/module/account"
	"shopnexus/internal/module/catalog"
	"shopnexus/internal/module/chat"
	"shopnexus/internal/module/common"
	"shopnexus/internal/module/finance"
	"shopnexus/internal/module/observability"
	"shopnexus/internal/module/order"
	"shopnexus/internal/module/trust"
)

func main() {
	cfg, err := config.Load(validation.Default())
	if err != nil {
		log.Fatal(err)
	}

	// name doubles as the schema; the pool's search_path is set to it.
	type target struct {
		name string
		dsn  string
		fsys fs.FS
	}
	targets := []target{
		{"account", cfg.AccountDBDSN, account.Migrations()},
		{"catalog", cfg.CatalogDBDSN, catalog.Migrations()},
		{"chat", cfg.ChatDBDSN, chat.Migrations()},
		{"common", cfg.CommonDBDSN, common.Migrations()},
		{"order", cfg.OrderDBDSN, order.Migrations()},
		{"finance", cfg.FinanceDBDSN, finance.Migrations()},
		{"observability", cfg.ObservabilityDBDSN, observability.Migrations()},
		{"trust", cfg.TrustDBDSN, trust.Migrations()},
	}

	ctx := context.Background()
	for _, t := range targets {
		pool, err := postgres.NewPool(ctx, t.dsn, t.name)
		if err != nil {
			log.Fatalf("migrate %s: pool: %v", t.name, err)
		}
		// Create the schema before applying migrations; search_path already
		// points at it, so unqualified DDL (incl. schema_migrations) lands here.
		if _, err := pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+pgx.Identifier{t.name}.Sanitize()); err != nil {
			pool.Close()
			log.Fatalf("migrate %s: create schema: %v", t.name, err)
		}
		if err := postgres.Migrate(ctx, pool, t.fsys); err != nil {
			pool.Close()
			log.Fatalf("migrate %s: %v", t.name, err)
		}
		pool.Close()
		log.Printf("migrate %s: ok", t.name)
	}
}
