package seed

import (
	"context"
	"fmt"
	"log"

	"shopnexus-remastered/config"
	"shopnexus-remastered/internal/client/pgxpool"
	"shopnexus-remastered/internal/db"
	pgxsqlc "shopnexus-remastered/internal/utils/pgx/sqlc"

	"github.com/jaswdr/faker/v2"
)

// SeedConfig holds configuration for seeding
type SeedConfig struct {
	AccountCount      int
	ProductCount      int
	OrderCount        int
	PromotionCount    int
	CommentCount      int
	ClearExistingData bool
}

// DefaultSeedConfig returns default seeding configuration
func DefaultSeedConfig() *SeedConfig {
	return &SeedConfig{
		AccountCount:      100,
		ProductCount:      500,
		OrderCount:        200,
		PromotionCount:    20,
		CommentCount:      300,
		ClearExistingData: false,
	}
}

// NewDatabase creates a new database connection
func NewDatabase(cfg *config.Config) (*pgxsqlc.Storage, error) {
	pool, err := pgxpool.New(pgxpool.Options{
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
		return nil, err
	}

	return pgxsqlc.NewStorage(pool), nil
}

// SeedAll seeds all schemas with fake data
func SeedAll(ctx context.Context, storage db.Querier, fake *faker.Faker, cfg *SeedConfig) error {
	fmt.Println("🌱 Starting database seeding...")

	// Seed in dependency order
	accountData, err := SeedAccountSchema(ctx, storage, fake, cfg)
	if err != nil {
		return fmt.Errorf("failed to seed account schema: %w", err)
	}

	catalogData, err := SeedCatalogSchema(ctx, storage, fake, cfg, accountData)
	if err != nil {
		return fmt.Errorf("failed to seed catalog schema: %w", err)
	}

	// Seed cart items (part of account schema but depends on catalog)
	err = SeedCartItems(ctx, storage, fake, cfg, accountData, catalogData)
	if err != nil {
		return fmt.Errorf("failed to seed cart items: %w", err)
	}

	inventoryData, err := SeedInventorySchema(ctx, storage, fake, cfg, catalogData)
	if err != nil {
		return fmt.Errorf("failed to seed inventory schema: %w", err)
	}

	paymentData, err := SeedPaymentSchema(ctx, storage, fake, cfg, accountData, catalogData, inventoryData)
	if err != nil {
		return fmt.Errorf("failed to seed payment schema: %w", err)
	}

	_, err = SeedPromotionSchema(ctx, storage, fake, cfg, paymentData)
	if err != nil {
		return fmt.Errorf("failed to seed promotion schema: %w", err)
	}

	_, err = SeedSharedSchema(ctx, storage, fake, cfg)
	if err != nil {
		return fmt.Errorf("failed to seed shared schema: %w", err)
	}

	_, err = SeedSystemSchema(ctx, storage, fake, cfg, accountData)
	if err != nil {
		return fmt.Errorf("failed to seed system schema: %w", err)
	}

	fmt.Println("✅ Database seeding completed successfully!")
	return nil
}

// Example function to demonstrate usage
func Example() {
	storage, err := NewDatabase(config.GetConfig())
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}

	fake := faker.New()
	cfg := DefaultSeedConfig()
	ctx := context.Background()

	if err := SeedAll(ctx, storage, &fake, cfg); err != nil {
		log.Fatal("Failed to seed database:", err)
	}
}
