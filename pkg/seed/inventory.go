package seed

import (
	"context"
	"fmt"
	"time"

	"shopnexus-remastered/internal/db"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jaswdr/faker/v2"
)

// InventorySeedData holds seeded inventory data for other seeders to reference
type InventorySeedData struct {
	ProductSerials []db.InventorySkuSerial
	Stocks         []db.InventoryStock
	StockHistories []db.InventoryStockHistory
}

// SeedInventorySchema seeds the inventory schema with fake data
func SeedInventorySchema(ctx context.Context, storage db.Querier, fake *faker.Faker, cfg *SeedConfig, catalogData *CatalogSeedData) (*InventorySeedData, error) {
	fmt.Println("📦 Seeding inventory schema...")

	data := &InventorySeedData{
		ProductSerials: make([]db.InventorySkuSerial, 0),
		Stocks:         make([]db.InventoryStock, 0),
		StockHistories: make([]db.InventoryStockHistory, 0),
	}

	if len(catalogData.ProductSkus) == 0 {
		fmt.Println("⚠️ No product SKUs found, skipping inventory seeding")
		return data, nil
	}

	// Create stock records for each product SKU
	for _, sku := range catalogData.ProductSkus {
		currentStock := int64(fake.RandomDigit()%200 + 10) // 10-209 items in stock
		sold := int64(fake.RandomDigit() % 50)             // 0-49 items sold

		stock, err := storage.CreateStock(ctx, db.CreateStockParams{
			RefType:      db.InventoryStockTypeProductSKU,
			RefID:        sku.ID,
			CurrentStock: currentStock,
			Sold:         sold,
			DateCreated:  pgtype.Timestamptz{Time: time.Now(), Valid: true},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create stock for SKU %d: %w", sku.ID, err)
		}
		data.Stocks = append(data.Stocks, stock)

		// Create 2-5 stock history entries for each stock
		historyCount := fake.RandomDigit()%4 + 2
		for i := 0; i < historyCount; i++ {
			change := int64(fake.RandomDigit()%100 + 1) // Positive number (stock added)
			if fake.Boolean().Bool() {
				change = -change // Negative number (stock removed)
			}

			history, err := storage.CreateStockHistory(ctx, db.CreateStockHistoryParams{
				StockID:     stock.ID,
				Change:      change,
				DateCreated: pgtype.Timestamptz{Time: time.Now().Add(-time.Duration(fake.RandomDigit()%720) * time.Hour), Valid: true}, // Within last 30 days
			})
			if err != nil {
				return nil, fmt.Errorf("failed to create stock history: %w", err)
			}
			data.StockHistories = append(data.StockHistories, history)
		}

		// Create serial numbers for some products (typically electronics, valuable items)
		// Let's say 30% of products have serial numbers
		if fake.RandomDigit()%10 < 3 {
			serialCount := int(currentStock)
			if serialCount > 50 { // Limit to avoid too many serials
				serialCount = 50
			}

			statuses := db.AllInventoryProductStatusValues()
			for j := 0; j < serialCount; j++ {
				var status = statuses[fake.RandomDigit()%len(statuses)]

				serial, err := retryWithUniqueValues(3, func(attempt int) (db.InventorySkuSerial, error) {
					return storage.CreateSkuSerial(ctx, db.CreateSkuSerialParams{
						SerialNumber: generateUniqueSerialNumber(fake),
						SkuID:        sku.ID,
						Status:       status,
						DateCreated:  pgtype.Timestamptz{Time: time.Now(), Valid: true},
					})
				})
				if err != nil {
					return nil, fmt.Errorf("failed to create product serial: %w", err)
				}
				data.ProductSerials = append(data.ProductSerials, serial)
			}
		}
	}

	fmt.Printf("✅ Inventory schema seeded: %d product serials, %d stocks, %d stock histories\n",
		len(data.ProductSerials), len(data.Stocks), len(data.StockHistories))

	return data, nil
}

// generateSerialNumber creates realistic serial numbers
func generateSerialNumber(fake *faker.Faker) string {
	// Generate different types of serial numbers
	serialTypes := []func() string{
		func() string { // Format: ABC123456789
			letters := "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
			prefix := ""
			for i := 0; i < 3; i++ {
				prefix += string(letters[fake.RandomDigit()%len(letters)])
			}
			numbers := ""
			for i := 0; i < 9; i++ {
				numbers += fmt.Sprintf("%d", fake.RandomDigit())
			}
			return prefix + numbers
		},
		func() string { // Format: 1234-5678-9012
			part1 := ""
			part2 := ""
			part3 := ""
			for i := 0; i < 4; i++ {
				part1 += fmt.Sprintf("%d", fake.RandomDigit())
				part2 += fmt.Sprintf("%d", fake.RandomDigit())
				part3 += fmt.Sprintf("%d", fake.RandomDigit())
			}
			return part1 + "-" + part2 + "-" + part3
		},
		func() string { // Format: SN20241234567890
			year := 2024
			numbers := ""
			for i := 0; i < 10; i++ {
				numbers += fmt.Sprintf("%d", fake.RandomDigit())
			}
			return fmt.Sprintf("SN%d%s", year, numbers)
		},
	}

	serialType := serialTypes[fake.RandomDigit()%len(serialTypes)]
	return serialType()
}
