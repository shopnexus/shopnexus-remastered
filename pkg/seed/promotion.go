package seed

import (
	"context"
	"fmt"
	"time"

	"shopnexus-remastered/internal/db"
	"shopnexus-remastered/internal/utils/ptr"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jaswdr/faker/v2"
)

// PromotionSeedData holds seeded promotion data for other seeders to reference
type PromotionSeedData struct {
	Promotions           []db.PromotionPromotion
	PromotionVouchers    []db.PromotionPromotionVoucher
	PromotionRedemptions []db.PromotionPromotionRedemption
}

// SeedPromotionSchema seeds the promotion schema with fake data
func SeedPromotionSchema(ctx context.Context, storage db.Querier, fake *faker.Faker, cfg *SeedConfig, paymentData *PaymentSeedData) (*PromotionSeedData, error) {
	fmt.Println("🎁 Seeding promotion schema...")

	data := &PromotionSeedData{
		Promotions:           make([]db.PromotionPromotion, 0),
		PromotionVouchers:    make([]db.PromotionPromotionVoucher, 0),
		PromotionRedemptions: make([]db.PromotionPromotionRedemption, 0),
	}

	promotionTypes := db.AllPromotionPromotionTypeValues()

	// Create promotions
	for i := 0; i < cfg.PromotionCount; i++ {
		promotionType := promotionTypes[fake.RandomDigit()%len(promotionTypes)]

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

		promotion, err := retryWithUniqueValues(3, func(attempt int) (db.PromotionPromotion, error) {
			return storage.CreatePromotion(ctx, db.CreatePromotionParams{
				Code:        generatePromotionCode(fake, promotionType),
				Type:        promotionType,
				IsActive:    isActive,
				DateStarted: pgtype.Timestamptz{Time: startDate, Valid: true},
				DateEnded:   pgtype.Timestamptz{Time: endDate, Valid: true},
				DateCreated: pgtype.Timestamptz{Time: now, Valid: true},
			})
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create promotion %d: %w", i+1, err)
		}
		data.Promotions = append(data.Promotions, promotion)

		// Create voucher details for Voucher type promotions
		if promotionType == "Voucher" {
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

			voucher, err := storage.CreatePromotionVoucher(ctx, db.CreatePromotionVoucherParams{
				PromotionID:     promotion.ID,
				MinSpend:        minSpend,
				MaxDiscount:     maxDiscount,
				DiscountPercent: pgtype.Int4{Int32: ptr.DerefDefault(discountPercent, 0), Valid: discountPercent != nil},
				DiscountPrice:   pgtype.Int8{Int64: ptr.DerefDefault(discountPrice, 0), Valid: discountPrice != nil},
			})
			if err != nil {
				return nil, fmt.Errorf("failed to create promotion voucher: %w", err)
			}
			data.PromotionVouchers = append(data.PromotionVouchers, voucher)
		}

		// Create redemptions for active/expired promotions
		if len(paymentData.Orders) > 0 && len(paymentData.OrderItems) > 0 {
			redemptionCount := fake.RandomDigit()%5 + 1 // 1-5 redemptions per promotion

			for j := 0; j < redemptionCount && j < len(paymentData.OrderItems); j++ {
				orderItem := paymentData.OrderItems[fake.RandomDigit()%len(paymentData.OrderItems)]

				refTypes := db.AllPromotionPromotionRefTypeValues()
				refType := refTypes[fake.RandomDigit()%len(refTypes)]

				var refID int64
				if refType == "OrderItem" {
					refID = orderItem.ID
				} else {
					refID = orderItem.OrderID
				}

				// Only create redemptions for orders that could have used this promotion
				order := getOrderByID(paymentData.Orders, orderItem.OrderID)
				if order != nil && order.DateCreated.Time.After(startDate) && order.DateCreated.Time.Before(endDate.Add(time.Hour*24)) {
					redemption, err := storage.CreatePromotionRedemption(ctx, db.CreatePromotionRedemptionParams{
						PromotionID: promotion.ID,
						Version:     1, // Simple version tracking
						RefType:     refType,
						RefID:       refID,
						DateCreated: pgtype.Timestamptz{Time: time.Now(), Valid: true},
					})
					if err != nil {
						return nil, fmt.Errorf("failed to create promotion redemption: %w", err)
					}
					data.PromotionRedemptions = append(data.PromotionRedemptions, redemption)
				}
			}
		}
	}

	fmt.Printf("✅ Promotion schema seeded: %d promotions, %d vouchers, %d redemptions\n",
		len(data.Promotions), len(data.PromotionVouchers), len(data.PromotionRedemptions))

	return data, nil
}

// Helper function to get order by ID
func getOrderByID(orders []db.PaymentOrder, id int64) *db.PaymentOrder {
	for _, order := range orders {
		if order.ID == id {
			return &order
		}
	}
	return nil
}

// generatePromotionCode creates realistic promotion codes
func generatePromotionCode(fake *faker.Faker, promotionType db.PromotionPromotionType) string {
	prefixes := map[db.PromotionPromotionType][]string{
		"Voucher":   {"SAVE", "DISCOUNT", "DEAL", "OFFER", "COUPON"},
		"FlashSale": {"FLASH", "QUICK", "FAST", "SPEED", "RUSH"},
		"Bundle":    {"BUNDLE", "COMBO", "PACK", "SET", "GROUP"},
		"BuyXGetY":  {"BOGO", "BUY", "GET", "FREE", "BONUS"},
		"Cashback":  {"CASH", "BACK", "RETURN", "REFUND", "MONEY"},
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
