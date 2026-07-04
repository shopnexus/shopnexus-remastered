package main

import (
	"context"
	"encoding/json"
	"log"
	"os"

	"github.com/brianvoe/gofakeit/v7"

	"shopnexus-server/internal/infras/pg"
	accountconfig "shopnexus-server/internal/module/account/config"
	accountdb "shopnexus-server/internal/module/account/db/sqlc"
	catalogdb "shopnexus-server/internal/module/catalog/db/sqlc"
	commondb "shopnexus-server/internal/module/common/db/sqlc"
	inventorydb "shopnexus-server/internal/module/inventory/db/sqlc"
	promotiondb "shopnexus-server/internal/module/promotion/db/sqlc"
)

func main() {
	ctx := context.Background()
	cfg, err := accountconfig.NewConfig()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	fake := gofakeit.New(0)

	pool, err := pg.New(pg.Options{
		Url:             cfg.Postgres.Url,
		Host:            cfg.Postgres.Host,
		Port:            cfg.Postgres.Port,
		Username:        cfg.Postgres.Username,
		Password:        cfg.Postgres.Password,
		Database:        cfg.Postgres.Database,
		MaxConnections:  cfg.Postgres.MaxConnections,
		MaxConnIdleTime: cfg.Postgres.MaxConnIdleTime,
	})
	if err != nil {
		log.Fatalf("failed to connect to postgres: %v", err)
	}
	defer pool.Close()

	// Load product data
	data, err := os.ReadFile("assets/data.json")
	if err != nil {
		log.Fatalf("failed to read data.json: %v", err)
	}

	var products []InputProduct
	if err := json.Unmarshal(data, &products); err != nil {
		log.Fatalf("failed to parse data.json: %v", err)
	}

	log.Printf("Loaded %d products from data.json", len(products))

	// Single transaction for the entire seed
	tx, err := pool.Begin(ctx)
	if err != nil {
		log.Fatalf("failed to begin transaction: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	accountStore := accountdb.New(tx)
	catalogStore := catalogdb.New(tx)
	commonStore := commondb.New(tx)
	inventoryStore := inventorydb.New(tx)
	promotionStore := promotiondb.New(tx)

	// Step 1: Create vendor accounts
	accounts, err := createAccounts(ctx, accountStore, fake)
	if err != nil {
		log.Fatalf("failed to create accounts: %v", err)
	}

	log.Printf("Created %d vendor accounts:", len(accounts))
	for _, a := range accounts {
		log.Printf("  %s | %s | password: %s", a.Username, a.Email, a.Password)
	}

	// Step 2: Seed base categories
	catIdx, err := seedCategories(ctx, catalogStore)
	if err != nil {
		log.Fatalf("failed to seed categories: %v", err)
	}

	// Step 3: Process products
	for i, product := range products {
		owner := accounts[i%len(accounts)]

		if err := processProduct(ctx, fake, product, owner.ID, catIdx, accounts,
			accountStore, catalogStore, commonStore, inventoryStore, promotionStore,
		); err != nil {
			log.Fatalf("failed to process product %d (%s): %v", i+1, product.Title, err)
		}

		if (i+1)%100 == 0 {
			log.Printf("Progress: %d/%d products processed", i+1, len(products))
		}
	}

	// Commit everything
	if err := tx.Commit(ctx); err != nil {
		log.Fatalf("failed to commit transaction: %v", err)
	}

	log.Printf("Successfully seeded %d products", len(products))
}
