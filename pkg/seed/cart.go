package seed

import (
	"context"
	"fmt"
	"time"

	"shopnexus-remastered/internal/db"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jaswdr/faker/v2"
)

// SeedCartItems creates cart items for customers (part of account schema)
func SeedCartItems(ctx context.Context, storage db.Querier, fake *faker.Faker, cfg *SeedConfig, accountData *AccountSeedData, catalogData *CatalogSeedData) error {
	fmt.Println("🛒 Seeding cart items...")

	if len(accountData.Customers) == 0 || len(catalogData.ProductSkus) == 0 {
		fmt.Println("⚠️ No customers or product SKUs found, skipping cart items seeding")
		return nil
	}

	// Tạo unique tracker cho cart items (không cần unique constraints cho cart items thông thường)
	// tracker := NewUniqueTracker()

	// Prepare bulk cart item data
	var cartItemParams []db.CreateAccountCartItemParams

	// Create cart items for some customers (50% of customers have items in cart)
	for _, customer := range accountData.Customers {
		if fake.RandomDigit()%2 == 0 { // 50% chance
			continue
		}

		// Each customer has 1-5 items in cart
		itemCount := fake.RandomDigit()%5 + 1
		usedSkus := make(map[int64]bool) // Prevent duplicate SKUs in same cart

		for i := 0; i < itemCount; i++ {
			sku := catalogData.ProductSkus[fake.RandomDigit()%len(catalogData.ProductSkus)]

			// Skip if SKU already in cart
			if usedSkus[sku.ID] {
				continue
			}
			usedSkus[sku.ID] = true

			quantity := int64(fake.RandomDigit()%3 + 1) // 1-3 quantity

			cartItemParams = append(cartItemParams, db.CreateAccountCartItemParams{
				CartID:      customer.ID, // cart_id is customer.id
				SkuID:       sku.ID,
				Quantity:    quantity,
				DateUpdated: pgtype.Timestamptz{Time: time.Now(), Valid: true},
				DateCreated: pgtype.Timestamptz{Time: time.Now(), Valid: true},
			})
		}
	}

	// Bulk insert cart items
	if len(cartItemParams) > 0 {
		_, err := storage.CreateAccountCartItem(ctx, cartItemParams)
		if err != nil {
			return fmt.Errorf("failed to bulk create cart items: %w", err)
		}
	}

	fmt.Printf("✅ Cart items seeded: %d items\n", len(cartItemParams))
	return nil
}
