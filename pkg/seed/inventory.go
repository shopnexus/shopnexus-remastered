package seed

import (
	"context"
	"fmt"
	"shopnexus-remastered/internal/utils/pgutil"
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

	// Tạo unique tracker để theo dõi tính duy nhất
	tracker := NewUniqueTracker()

	data := &InventorySeedData{
		ProductSerials: make([]db.InventorySkuSerial, 0),
		Stocks:         make([]db.InventoryStock, 0),
		StockHistories: make([]db.InventoryStockHistory, 0),
	}

	if len(catalogData.ProductSkus) == 0 {
		fmt.Println("⚠️ No product SKUs found, skipping inventory seeding")
		return data, nil
	}

	// Prepare bulk stock data
	stockParams := make([]db.CreateInventoryStockParams, len(catalogData.ProductSkus))
	stockHistoryParams := make([]db.CreateInventoryStockHistoryParams, 0)
	serialParams := make([]db.CreateInventorySkuSerialParams, 0)

	baseStockID := int64(8000)

	for i, sku := range catalogData.ProductSkus {
		currentStock := int64(fake.RandomDigit()%200 + 10) // 10-209 items in stock
		sold := int64(fake.RandomDigit() % 50)             // 0-49 items sold

		stockParams[i] = db.CreateInventoryStockParams{
			RefType:      db.InventoryStockTypeProductSKU,
			RefID:        sku.ID,
			CurrentStock: currentStock,
			Sold:         sold,
			DateCreated:  pgtype.Timestamptz{Time: time.Now(), Valid: true},
		}

		// Prepare stock history for this stock
		stockID := baseStockID + int64(i)
		historyCount := fake.RandomDigit()%4 + 2 // 2-5 history entries
		for j := 0; j < historyCount; j++ {
			change := int64(fake.RandomDigit()%100 + 1) // Positive number (stock added)
			if fake.Boolean().Bool() {
				change = -change // Negative number (stock removed)
			}

			stockHistoryParams = append(stockHistoryParams, db.CreateInventoryStockHistoryParams{
				StockID:     stockID,
				Change:      change,
				DateCreated: pgtype.Timestamptz{Time: time.Now().Add(-time.Duration(fake.RandomDigit()%720) * time.Hour), Valid: true}, // Within last 30 days
			})
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

				serialParams = append(serialParams, db.CreateInventorySkuSerialParams{
					SerialNumber: generateUniqueSerialNumberWithTracker(fake, tracker),
					SkuID:        sku.ID,
					Status:       status,
					DateCreated:  pgtype.Timestamptz{Time: time.Now(), Valid: true},
				})
			}
		}
	}

	// Bulk insert stocks
	_, err := storage.CreateInventoryStock(ctx, stockParams)
	if err != nil {
		return nil, fmt.Errorf("failed to bulk create stocks: %w", err)
	}

	// Query back created stocks
	stocks, err := storage.ListInventoryStock(ctx, db.ListInventoryStockParams{
		Limit:  pgutil.Int32ToPgInt4(int32(len(stockParams) * 2)),
		Offset: pgutil.Int32ToPgInt4(0),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query back created stocks: %w", err)
	}

	// Match stocks with SKUs by RefID (SKU ID)
	stockRefMap := make(map[int64]db.InventoryStock)
	for _, stock := range stocks {
		stockRefMap[stock.RefID] = stock
	}

	// Populate data.Stocks with actual database records
	for _, params := range stockParams {
		if stock, exists := stockRefMap[params.RefID]; exists {
			data.Stocks = append(data.Stocks, stock)
		}
	}

	// Update stock history parameters with actual stock IDs
	// We need to map the temporary stock IDs to real stock IDs
	for i := range stockHistoryParams {
		tempStockIndex := int(stockHistoryParams[i].StockID - baseStockID)
		if tempStockIndex >= 0 && tempStockIndex < len(catalogData.ProductSkus) {
			skuID := catalogData.ProductSkus[tempStockIndex].ID
			if stock, exists := stockRefMap[skuID]; exists {
				stockHistoryParams[i].StockID = stock.ID
			}
		}
	}

	// Bulk insert stock histories
	if len(stockHistoryParams) > 0 {
		// Filter out histories without valid stock IDs
		validHistoryParams := make([]db.CreateInventoryStockHistoryParams, 0)
		for _, history := range stockHistoryParams {
			if history.StockID > 0 {
				validHistoryParams = append(validHistoryParams, history)
			}
		}

		if len(validHistoryParams) > 0 {
			_, err = storage.CreateInventoryStockHistory(ctx, validHistoryParams)
			if err != nil {
				return nil, fmt.Errorf("failed to bulk create stock histories: %w", err)
			}

			// Query back created stock histories
			stockHistories, err := storage.ListInventoryStockHistory(ctx, db.ListInventoryStockHistoryParams{
				Limit:  pgutil.Int32ToPgInt4(int32(len(validHistoryParams) * 2)),
				Offset: pgutil.Int32ToPgInt4(0),
			})
			if err != nil {
				return nil, fmt.Errorf("failed to query back created stock histories: %w", err)
			}

			// Populate data.StockHistories with actual database records
			data.StockHistories = stockHistories
		}
	}

	// Bulk insert product serials
	if len(serialParams) > 0 {
		_, err = storage.CreateInventorySkuSerial(ctx, serialParams)
		if err != nil {
			return nil, fmt.Errorf("failed to bulk create product serials: %w", err)
		}

		// Query back created product serials
		productSerials, err := storage.ListInventorySkuSerial(ctx, db.ListInventorySkuSerialParams{
			Limit:  pgutil.Int32ToPgInt4(int32(len(serialParams) * 2)),
			Offset: pgutil.Int32ToPgInt4(0),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to query back created product serials: %w", err)
		}

		// Match serials with our parameters by serial number (unique identifier)
		serialNumberMap := make(map[string]db.InventorySkuSerial)
		for _, serial := range productSerials {
			serialNumberMap[serial.SerialNumber] = serial
		}

		// Populate data.ProductSerials with actual database records
		for _, params := range serialParams {
			if serial, exists := serialNumberMap[params.SerialNumber]; exists {
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
