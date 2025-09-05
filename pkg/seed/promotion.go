package seed

import (
	"context"
	"fmt"
	"shopnexus-remastered/internal/utils/pgutil"
	"time"

	"shopnexus-remastered/internal/db"
	"shopnexus-remastered/internal/utils/ptr"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jaswdr/faker/v2"
)

// PromotionSeedData holds seeded promotion data for other seeders to reference
type PromotionSeedData struct {
	Promotions         []db.PromotionBase
	PromotionDiscounts []db.PromotionDiscount
}

// SeedPromotionSchema seeds the promotion schema with fake data
func SeedPromotionSchema(ctx context.Context, storage db.Querier, fake *faker.Faker, cfg *SeedConfig, accountData *AccountSeedData, catalogData *CatalogSeedData) (*PromotionSeedData, error) {
	fmt.Println("🎁 Seeding promotion schema...")

	// Tạo unique tracker để theo dõi tính duy nhất
	tracker := NewUniqueTracker()

	data := &PromotionSeedData{
		Promotions:         make([]db.PromotionBase, 0),
		PromotionDiscounts: make([]db.PromotionDiscount, 0),
	}

	promotionTypes := db.AllPromotionTypeValues()
	promotionRefTypes := db.AllPromotionRefTypeValues()

	// Prepare bulk promotion data
	promotionParams := make([]db.CreatePromotionBaseParams, cfg.PromotionCount)
	discountParams := make([]db.CreatePromotionDiscountParams, 0)

	for i := 0; i < cfg.PromotionCount; i++ {
		promotionType := promotionTypes[fake.RandomDigit()%len(promotionTypes)]
		promotionRefType := promotionRefTypes[fake.RandomDigit()%len(promotionRefTypes)]

		// Create promotion period (some are active, some are expired, some are future)
		var startDate, endDate time.Time
		now := time.Now()

		switch fake.RandomDigit() % 3 {
		case 0: // Active promotion
			startDate = now.AddDate(0, 0, -fake.RandomDigit()%30) // Started up to 30 days ago
			endDate = now.AddDate(0, 0, fake.RandomDigit()%60+1)  // Ends in 1-60 days
		case 1: // Expired promotion
			startDate = now.AddDate(0, 0, -fake.RandomDigit()%90-30) // Started 30-120 days ago
			endDate = now.AddDate(0, 0, -fake.RandomDigit()%30-1)    // Ended 1-30 days ago
		case 2: // Future promotion
			startDate = now.AddDate(0, 0, fake.RandomDigit()%30+1)     // Starts in 1-30 days
			endDate = startDate.AddDate(0, 0, fake.RandomDigit()%60+7) // Lasts 7-67 days
		}

		isActive := now.After(startDate) && now.Before(endDate)

		// Determine owner_id and ref_id based on promotion ref type
		var ownerID *int64
		var refID *int64

		switch promotionRefType {
		case "All":
			// System promotion, no specific owner or ref
			ownerID = nil
			refID = nil
		case "ProductSpu":
			// Vendor-owned promotion for specific product
			if len(accountData.Vendors) > 0 && len(catalogData.ProductSpus) > 0 {
				vendor := accountData.Vendors[fake.RandomDigit()%len(accountData.Vendors)]
				spu := catalogData.ProductSpus[fake.RandomDigit()%len(catalogData.ProductSpus)]
				ownerID = &vendor.ID
				refID = &spu.ID
			}
		case "ProductSku":
			// Vendor-owned promotion for specific SKU
			if len(accountData.Vendors) > 0 && len(catalogData.ProductSkus) > 0 {
				vendor := accountData.Vendors[fake.RandomDigit()%len(accountData.Vendors)]
				sku := catalogData.ProductSkus[fake.RandomDigit()%len(catalogData.ProductSkus)]
				ownerID = &vendor.ID
				refID = &sku.ID
			}
		case "Category":
			// Vendor-owned promotion for category
			if len(accountData.Vendors) > 0 && len(catalogData.Categories) > 0 {
				vendor := accountData.Vendors[fake.RandomDigit()%len(accountData.Vendors)]
				category := catalogData.Categories[fake.RandomDigit()%len(catalogData.Categories)]
				ownerID = &vendor.ID
				refID = &category.ID
			}
		case "Brand":
			// Vendor-owned promotion for brand
			if len(accountData.Vendors) > 0 && len(catalogData.Brands) > 0 {
				vendor := accountData.Vendors[fake.RandomDigit()%len(accountData.Vendors)]
				brand := catalogData.Brands[fake.RandomDigit()%len(catalogData.Brands)]
				ownerID = &vendor.ID
				refID = &brand.ID
			}
		}

		// Generate title and description
		title := generatePromotionTitle(fake, promotionType)
		description := generatePromotionDescription(fake, promotionType)

		// Generate schedule fields for flash sale (Discount type with schedule)
		var scheduleTz *string
		var scheduleStart *time.Time
		var scheduleDuration *int32

		if promotionType == "Discount" && fake.Boolean().Bool() {
			// This is a flash sale - has schedule
			tz := "Asia/Ho_Chi_Minh" // Default timezone
			scheduleTz = &tz

			// Schedule start time (next few days)
			nextStart := now.AddDate(0, 0, fake.RandomDigit()%7+1)
			scheduleStart = &nextStart

			// Duration in minutes (30-480 minutes = 30min to 8hours)
			duration := int32(fake.RandomDigit()%450 + 30)
			scheduleDuration = &duration
		}

		promotionParams[i] = db.CreatePromotionBaseParams{
			Code:             generatePromotionCodeWithTracker(fake, promotionType, tracker),
			OwnerID:          pgtype.Int8{Int64: ptr.DerefDefault(ownerID, 0), Valid: ownerID != nil},
			RefType:          promotionRefType,
			RefID:            pgtype.Int8{Int64: ptr.DerefDefault(refID, 0), Valid: refID != nil},
			Type:             promotionType,
			Title:            title,
			Description:      pgtype.Text{String: description, Valid: description != ""},
			IsActive:         isActive,
			DateStarted:      pgtype.Timestamptz{Time: startDate, Valid: true},
			DateEnded:        pgtype.Timestamptz{Time: endDate, Valid: true},
			ScheduleTz:       pgtype.Text{String: ptr.DerefDefault(scheduleTz, ""), Valid: scheduleTz != nil},
			ScheduleStart:    pgtype.Timestamptz{Time: ptr.DerefDefault(scheduleStart, time.Time{}), Valid: scheduleStart != nil},
			ScheduleDuration: pgtype.Int4{Int32: ptr.DerefDefault(scheduleDuration, 0), Valid: scheduleDuration != nil},
			DateCreated:      pgtype.Timestamptz{Time: now, Valid: true},
			DateUpdated:      pgtype.Timestamptz{Time: now, Valid: true},
		}
	}

	// Bulk insert promotions
	_, err := storage.CreatePromotionBase(ctx, promotionParams)
	if err != nil {
		return nil, fmt.Errorf("failed to bulk create promotions: %w", err)
	}

	// Query back created promotions
	promotions, err := storage.ListPromotionBase(ctx, db.ListPromotionBaseParams{
		Limit:  pgutil.Int32ToPgInt4(int32(len(promotionParams) * 2)),
		Offset: pgutil.Int32ToPgInt4(0),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query back created promotions: %w", err)
	}

	// Match promotions with our parameters by code (unique identifier)
	promotionCodeMap := make(map[string]db.PromotionBase)
	for _, promotion := range promotions {
		promotionCodeMap[promotion.Code] = promotion
	}

	// Populate data.Promotions with actual database records
	for _, params := range promotionParams {
		if promotion, exists := promotionCodeMap[params.Code]; exists {
			data.Promotions = append(data.Promotions, promotion)

			// Prepare discount details for Discount type promotions
			if params.Type == "Discount" {
				minSpend := int64(fake.RandomFloat(2, 100, 1000) * 100)  // $1-$10 minimum spend
				maxDiscount := int64(fake.RandomFloat(2, 50, 500) * 100) // $0.50-$5 max discount

				var discountPercent *int32
				var discountPrice *int64

				if fake.Boolean().Bool() {
					// Percentage discount
					percent := int32(fake.RandomDigit()%50 + 5) // 5-54% discount
					discountPercent = &percent
				} else {
					// Fixed price discount
					price := int64(fake.RandomFloat(2, 10, 100) * 100) // $0.10-$1 discount
					discountPrice = &price
				}

				discountParams = append(discountParams, db.CreatePromotionDiscountParams{
					ID:              promotion.ID,
					OrderWide:       fake.Bool(),
					MinSpend:        minSpend,
					MaxDiscount:     maxDiscount,
					DiscountPercent: pgtype.Int4{Int32: ptr.DerefDefault(discountPercent, 0), Valid: discountPercent != nil},
					DiscountPrice:   pgtype.Int8{Int64: ptr.DerefDefault(discountPrice, 0), Valid: discountPrice != nil},
				})
			}
		}
	}

	// Bulk insert discounts
	if len(discountParams) > 0 {
		_, err = storage.CreatePromotionDiscount(ctx, discountParams)
		if err != nil {
			return nil, fmt.Errorf("failed to bulk create promotion discounts: %w", err)
		}

		// Query back created discounts
		discounts, err := storage.ListPromotionDiscount(ctx, db.ListPromotionDiscountParams{
			Limit:  pgutil.Int32ToPgInt4(int32(len(discountParams) * 2)),
			Offset: pgutil.Int32ToPgInt4(0),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to query back created discounts: %w", err)
		}

		// Match discounts with promotions by ID
		discountPromotionMap := make(map[int64]db.PromotionDiscount)
		for _, discount := range discounts {
			discountPromotionMap[discount.ID] = discount
		}

		// Populate data.PromotionDiscounts with actual database records
		for _, params := range discountParams {
			if discount, exists := discountPromotionMap[params.ID]; exists {
				data.PromotionDiscounts = append(data.PromotionDiscounts, discount)
			}
		}
	}

	fmt.Printf("✅ Promotion schema seeded: %d promotions, %d discounts\n",
		len(data.Promotions), len(data.PromotionDiscounts))

	return data, nil
}

// generatePromotionCode creates realistic promotion codes
func generatePromotionCode(fake *faker.Faker, promotionType db.PromotionType) string {
	prefixes := map[db.PromotionType][]string{
		"Discount": {"SAVE", "DISCOUNT", "DEAL", "OFFER", "COUPON"},
		"Bundle":   {"BUNDLE", "COMBO", "PACK", "SET", "GROUP"},
		"BuyXGetY": {"BOGO", "BUY", "GET", "FREE", "BONUS"},
		"Cashback": {"CASH", "BACK", "RETURN", "REFUND", "MONEY"},
	}

	suffixes := []string{"10", "15", "20", "25", "50", "NOW", "TODAY", "VIP", "SPECIAL", "EXTRA"}

	var prefix string
	if prefixList, exists := prefixes[promotionType]; exists {
		prefix = prefixList[fake.RandomDigit()%len(prefixList)]
	} else {
		prefix = "PROMO"
	}

	suffix := suffixes[fake.RandomDigit()%len(suffixes)]

	return fmt.Sprintf("%s%s", prefix, suffix)
}

// generatePromotionCodeWithTracker creates realistic promotion codes với local checking
func generatePromotionCodeWithTracker(fake *faker.Faker, promotionType db.PromotionType, tracker *UniqueTracker) string {
	maxRetries := 100
	valueType := "PROMOTION_CODE"

	for attempt := 0; attempt < maxRetries; attempt++ {
		code := generatePromotionCode(fake, promotionType)
		if tracker.IsUnique(valueType, code) {
			tracker.Add(valueType, code)
			return code
		}
		time.Sleep(time.Microsecond * time.Duration(attempt+1))
	}

	// Fallback với timestamp nếu vẫn không unique
	timestamp := time.Now().UnixNano()
	code := fmt.Sprintf("PROMO_%d", timestamp%1000000)
	tracker.Add(valueType, code)
	return code
}

// generatePromotionTitle creates realistic promotion titles
func generatePromotionTitle(fake *faker.Faker, promotionType db.PromotionType) string {
	titles := map[db.PromotionType][]string{
		"Discount": {
			"Giảm giá đặc biệt",
			"Khuyến mãi hấp dẫn",
			"Ưu đãi lớn",
			"Sale off cuối tuần",
			"Flash sale giới hạn",
			"Voucher giảm giá",
			"Deal hot nhất",
		},
		"Bundle": {
			"Combo tiết kiệm",
			"Gói sản phẩm ưu đãi",
			"Bộ sưu tập đặc biệt",
			"Set combo giá tốt",
			"Gói deal hấp dẫn",
		},
		"BuyXGetY": {
			"Mua 2 tặng 1",
			"Mua 1 tặng 1",
			"Mua nhiều giảm nhiều",
			"Ưu đãi mua kèm",
			"Deal mua combo",
		},
		"Cashback": {
			"Hoàn tiền hấp dẫn",
			"Cashback đặc biệt",
			"Ưu đãi hoàn tiền",
			"Tiền mặt trả lại",
			"Deal cashback",
		},
	}

	if titleList, exists := titles[promotionType]; exists {
		return titleList[fake.RandomDigit()%len(titleList)]
	}
	return "Khuyến mãi đặc biệt"
}

// generatePromotionDescription creates realistic promotion descriptions
func generatePromotionDescription(fake *faker.Faker, promotionType db.PromotionType) string {
	descriptions := map[db.PromotionType][]string{
		"Discount": {
			"Áp dụng cho tất cả sản phẩm trong danh mục",
			"Giảm giá cho khách hàng VIP",
			"Ưu đãi đặc biệt trong thời gian có hạn",
			"Flash sale chỉ trong vài giờ",
			"Giảm giá sâu cho sản phẩm hot",
			"Voucher có thể sử dụng nhiều lần",
		},
		"Bundle": {
			"Mua combo tiết kiệm hơn mua lẻ",
			"Gói sản phẩm được tuyển chọn kỹ lưỡng",
			"Combo đa dạng cho mọi nhu cầu",
			"Bộ sưu tập độc quyền",
			"Gói deal không thể bỏ qua",
		},
		"BuyXGetY": {
			"Mua nhiều được tặng nhiều",
			"Ưu đãi mua kèm hấp dẫn",
			"Deal mua combo tiết kiệm",
			"Tặng kèm sản phẩm giá trị",
			"Mua 1 tặng 1 - deal không thể bỏ qua",
		},
		"Cashback": {
			"Hoàn tiền ngay sau khi mua",
			"Cashback cao nhất thị trường",
			"Ưu đãi hoàn tiền không giới hạn",
			"Tiền mặt trả lại nhanh chóng",
			"Deal cashback hấp dẫn",
		},
	}

	if descList, exists := descriptions[promotionType]; exists {
		return descList[fake.RandomDigit()%len(descList)]
	}
	return "Khuyến mãi đặc biệt dành cho bạn"
}
