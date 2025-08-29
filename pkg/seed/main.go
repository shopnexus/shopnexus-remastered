package seed

import (
	"context"
	"log"

	"shopnexus-remastered/config"

	"github.com/jaswdr/faker/v2"
)

// Main seeding function that can be called from anywhere
func Main() {
	storage, err := NewDatabase(config.GetConfig())
	if err != nil {
		log.Fatal("❌ Failed to connect to database:", err)
	}

	fake := faker.New()
	cfg := DefaultSeedConfig()
	ctx := context.Background()

	log.Println("🌱 Starting ShopNexus database seeding...")
	log.Printf("📊 Configuration: %d accounts, %d products, %d orders, %d promotions, %d comments",
		cfg.AccountCount, cfg.ProductCount, cfg.OrderCount, cfg.PromotionCount, cfg.CommentCount)

	txStorage, err := storage.BeginTx(ctx)
	if err != nil {
		log.Fatal("❌ Failed to begin transaction:", err)
	}

	// Ensure rollback on error
	defer func() {
		if r := recover(); r != nil {
			txStorage.Rollback(ctx)
			log.Fatal("❌ Panic during seeding:", r)
		}
	}()

	if err := SeedAll(ctx, txStorage, &fake, cfg); err != nil {
		txStorage.Rollback(ctx)
		log.Fatal("❌ Failed to seed database:", err)
	}

	if err := txStorage.Commit(ctx); err != nil {
		log.Fatal("❌ Failed to commit transaction:", err)
	}

	log.Println("🎉 ShopNexus database seeding completed successfully!")
}

// MainWithCustomConfig allows seeding with custom configuration
func MainWithCustomConfig(cfg *SeedConfig) {
	storage, err := NewDatabase(config.GetConfig())
	if err != nil {
		log.Fatal("❌ Failed to connect to database:", err)
	}

	fake := faker.New()
	ctx := context.Background()

	log.Println("🌱 Starting ShopNexus database seeding with custom config...")
	log.Printf("📊 Configuration: %d accounts, %d products, %d orders, %d promotions, %d comments",
		cfg.AccountCount, cfg.ProductCount, cfg.OrderCount, cfg.PromotionCount, cfg.CommentCount)

	//txStorage, err := storage.BeginTx(ctx)
	//if err != nil {
	//	log.Fatal("❌ Failed to begin transaction:", err)
	//}

	// Ensure rollback on error
	defer func() {
		if r := recover(); r != nil {
			//txStorage.Rollback(ctx)
			log.Fatal("❌ Panic during seeding:", r)
		}
	}()

	if err := SeedAll(ctx, storage, &fake, cfg); err != nil {
		//txStorage.Rollback(ctx)
		log.Fatal("❌ Failed to seed database:", err)
	}

	//if err := txStorage.Commit(ctx); err != nil {
	//	log.Fatal("❌ Failed to commit transaction:", err)
	//}

	log.Println("🎉 ShopNexus database seeding completed successfully!")
}
