// Command seed fills a migrated database with browsable development data: five accounts
// that each sell and buy, the category tree, the listings in assets/data.json with their
// variants, stock, photos and tags, and the completed orders that back the product reviews.
// Run it after cmd/migrate — it refuses to run a second time, because there is no way to
// tell a seeded row from a real one afterwards.
//
// It writes SQL directly, one transaction per module pool, rather than going through the
// services. There is no "create this listing as that seller with that rating" command and
// there should not be: a rating is earned, and inventing the history that earned it is this
// command's whole job.
//
// What it deliberately leaves out is the finance ledger: a seeded order has no payment
// session, no escrow movement and no wallet entry. Catalog, order and trust agree with each
// other; finance is empty. Everything read-only — browse, search, a shop page, a product
// page with its reviews — works. Anything that reads the money behind a seeded order does not.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"shopnexus/internal/config"
	"shopnexus/internal/infra/postgres"
	"shopnexus/internal/shared/validation"
)

// productsPath is relative to the repository root, which is where this command is run from
// — the same assumption `go run ./cmd/seed` already makes.
const productsPath = "assets/data.json"

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatalf("seed: %v", err)
	}
}

func run(ctx context.Context) error {
	cfg, err := config.Load(validation.Default())
	if err != nil {
		return err
	}

	products, err := loadProducts(productsPath)
	if err != nil {
		return err
	}
	categories, err := loadCategories()
	if err != nil {
		return err
	}
	p := buildPlan(products, categories)
	log.Printf("planned %d listings, %d variants, %d photos, %d sales",
		len(p.listings), countVariants(p), len(p.images), countSales(p))

	db, err := openPools(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.close()

	if err := checkNotSeeded(ctx, db.account); err != nil {
		return err
	}

	start := time.Now()
	sellers, err := writeAccounts(ctx, db.account)
	if err != nil {
		return fmt.Errorf("seed accounts: %w", err)
	}
	log.Printf("accounts: %d", len(sellers))

	cat, err := writeCatalog(ctx, db.catalog, p, sellers)
	if err != nil {
		return fmt.Errorf("seed catalog: %w", err)
	}
	log.Printf("catalog: %d categories, %d listings", len(p.categories), len(cat.listings))

	orders, err := writeSales(ctx, db.order, p, sellers, cat)
	if err != nil {
		return fmt.Errorf("seed orders: %w", err)
	}
	if err := writeTrust(ctx, db.trust, p, sellers, cat, orders); err != nil {
		return fmt.Errorf("seed trust: %w", err)
	}
	log.Printf("sales: %d completed orders, each with its review", countSales(p))

	log.Printf("done in %s. sign in with:", time.Since(start).Round(time.Millisecond))
	for _, a := range seedAccounts {
		log.Printf("  %-12s %-22s %s", a.Username, a.Email, a.Password)
	}
	return nil
}

// Each module gets its own pool on its own DSN with its own search_path, exactly as
// cmd/migrate and the running gateway do. In dev all four point at one database and are
// kept apart by schema.
type pools struct {
	account *pgxpool.Pool
	catalog *pgxpool.Pool
	order   *pgxpool.Pool
	trust   *pgxpool.Pool
}

func openPools(ctx context.Context, cfg *config.Config) (*pools, error) {
	out := &pools{}
	for _, target := range []struct {
		name string
		dsn  string
		dst  **pgxpool.Pool
	}{
		{"account", cfg.AccountDBDSN, &out.account},
		{"catalog", cfg.CatalogDBDSN, &out.catalog},
		{"order", cfg.OrderDBDSN, &out.order},
		{"trust", cfg.TrustDBDSN, &out.trust},
	} {
		pool, err := postgres.NewPool(ctx, target.dsn, target.name)
		if err != nil {
			out.close()
			return nil, fmt.Errorf("open %s pool: %w", target.name, err)
		}
		*target.dst = pool
	}
	return out, nil
}

func (p *pools) close() {
	for _, pool := range []*pgxpool.Pool{p.account, p.catalog, p.order, p.trust} {
		if pool != nil {
			pool.Close()
		}
	}
}

func countVariants(p *plan) int {
	n := 0
	for _, l := range p.listings {
		n += len(l.variants)
	}
	return n
}

func countSales(p *plan) int {
	n := 0
	for _, l := range p.listings {
		n += len(l.sales)
	}
	return n
}
