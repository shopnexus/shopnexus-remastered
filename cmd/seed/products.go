package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/brianvoe/gofakeit/v7"
	"github.com/google/uuid"
	"github.com/gosimple/slug"
	null "github.com/guregu/null/v6"

	accountdb "shopnexus-server/internal/module/account/db/sqlc"
	catalogdb "shopnexus-server/internal/module/catalog/db/sqlc"
	commondb "shopnexus-server/internal/module/common/db/sqlc"
	inventorydb "shopnexus-server/internal/module/inventory/db/sqlc"
	promotiondb "shopnexus-server/internal/module/promotion/db/sqlc"
)

func processProduct(
	ctx context.Context,
	fake *gofakeit.Faker,
	input InputProduct,
	accountID uuid.UUID,
	catIdx *categoryIndex,
	accounts []SeedAccount,
	accountStore *accountdb.Queries,
	catalogStore *catalogdb.Queries,
	commonStore *commondb.Queries,
	inventoryStore *inventorydb.Queries,
	promotionStore *promotiondb.Queries,
) error {
	// Match product to a base category via breadcrumb
	categoryID := catIdx.match(input.Breadcrumb)

	// Generate slug
	productSlug := slug.Make(input.Title)
	if productSlug == "" {
		productSlug = "product"
	}
	productSlug = fmt.Sprintf("%s.%s", productSlug, uuid.New().String()[:8])

	// Prepend brand as a specification
	specs := input.ProductSpecifications
	if input.Brand != "" {
		specs = append([]Spec{{Name: "Brand", Value: input.Brand}}, specs...)
	}

	specsJSON, err := json.Marshal(specs)
	if err != nil {
		specsJSON = []byte("[]")
	}

	// Create ProductSpu.
	// Currency is NOT NULL VARCHAR(3); fall back to VND if the JSON entry
	// is missing or malformed (e.g. a symbol like "$" instead of "USD").
	currency := strings.ToUpper(strings.TrimSpace(input.Currency))
	if len(currency) != 3 {
		return fmt.Errorf("invalid currency code: %s", input.Currency)
	}
	spu, err := catalogStore.CreateDefaultProductSpu(ctx, catalogdb.CreateDefaultProductSpuParams{
		Slug:           productSlug,
		AccountID:      accountID,
		CategoryID:     categoryID,
		Name:           input.Title,
		Description:    input.ProductDescription,
		IsEnabled:      strings.ToLower(input.IsAvailable) != "false",
		Currency:       currency,
		Specifications: specsJSON,
	})
	if err != nil {
		return fmt.Errorf("create product spu: %w", err)
	}

	// Generate SKU combinations
	variationCombos := generateVariationCombinations(input.Variations)
	if len(variationCombos) == 0 {
		variationCombos = [][]map[string]string{{}}
	}

	basePrice := int64(input.FinalPrice)
	if input.FinalPrice == 0 {
		basePrice = int64(input.InitialPrice)
	}

	totalStock := pickCurrentStock(input)
	sold := toBigInt(input.Sold)
	stockPerSku := max(totalStock/int64(len(variationCombos)), 1)
	soldPerSku := sold / int64(len(variationCombos))

	var featuredSkuID uuid.UUID
	for i, combo := range variationCombos {
		attributesJSON, err := json.Marshal(combo)
		if err != nil {
			attributesJSON = []byte("[]")
		}

		packageDetails := map[string]any{
			"weight_grams": fake.IntRange(100, 2000),
			"length_cm":    fake.IntRange(5, 50),
			"width_cm":     fake.IntRange(5, 50),
			"height_cm":    fake.IntRange(5, 50),
		}
		packageDetailsJSON, err := json.Marshal(packageDetails)
		if err != nil {
			packageDetailsJSON = []byte("{}")
		}

		price := basePrice + int64(fake.IntRange(-25, 50))
		if price < 1 {
			price = basePrice
		}

		sku, err := catalogStore.CreateDefaultProductSku(ctx, catalogdb.CreateDefaultProductSkuParams{
			SpuID:           spu.ID,
			Price:           price,
			SharedPackaging: false,
			Attributes:      attributesJSON,
			PackageDetails:  packageDetailsJSON,
		})
		if err != nil {
			return fmt.Errorf("create product sku: %w", err)
		}

		if i == 0 {
			featuredSkuID = sku.ID
		}

		stock, err := inventoryStore.CreateDefaultStock(ctx, inventorydb.CreateDefaultStockParams{
			RefType: inventorydb.InventoryStockRefTypeProductSku,
			RefID:   sku.ID,
			Stock:   stockPerSku,
		})
		if err != nil {
			return fmt.Errorf("create stock for sku: %w", err)
		}

		if soldPerSku > 0 {
			_, err = inventoryStore.UpdateStock(ctx, inventorydb.UpdateStockParams{
				ID:    stock.ID,
				Taken: null.IntFrom(soldPerSku),
			})
			if err != nil {
				return fmt.Errorf("update stock taken for sku: %w", err)
			}
		}
	}

	// Update featured_sku_id
	_, err = catalogStore.UpdateProductSpu(ctx, catalogdb.UpdateProductSpuParams{
		ID:                spu.ID,
		FeaturedSkuID:     uuid.NullUUID{UUID: featuredSkuID, Valid: true},
		NullFeaturedSkuID: false,
	})
	if err != nil {
		return fmt.Errorf("update featured sku: %w", err)
	}

	// Create resource references for images
	images := input.Image
	if len(images) > 10 {
		images = images[:10]
	}
	for order, imageURL := range images {
		if imageURL == "" {
			continue
		}

		// TODO: update this mime check
		mimeType := "image/jpeg"
		if strings.Contains(imageURL, ".png") {
			mimeType = "image/png"
		}

		resource, err := commonStore.GetResource(ctx, commondb.GetResourceParams{
			ID:        uuid.NullUUID{Valid: false},
			Provider:  null.StringFrom("remote"),
			ObjectKey: null.StringFrom(imageURL),
		})
		if err != nil {
			resource, err = commonStore.CreateDefaultResource(ctx, commondb.CreateDefaultResourceParams{
				UploadedByID: uuid.NullUUID{UUID: accountID, Valid: true},
				Provider:     "remote",
				ObjectKey:  imageURL,
				Mime:       mimeType,
				Size:       0,
				Metadata:   []byte("null"),
			})
			if err != nil {
				return fmt.Errorf("create resource for %s: %w", imageURL, err)
			}
		}

		_, err = commonStore.CreateDefaultResourceReference(ctx, commondb.CreateDefaultResourceReferenceParams{
			RsID:    resource.ID,
			RefType: commondb.CommonResourceRefTypeProductSpu,
			RefID:   spu.ID,
			Order:   int32(order),
		})
		if err != nil {
			return fmt.Errorf("create resource reference: %w", err)
		}
	}

	// Create search sync
	_, err = catalogStore.CreateSearchSync(ctx, catalogdb.CreateSearchSyncParams{
		RefType:          catalogdb.CatalogSearchSyncRefTypeProductSpu,
		RefID:            spu.ID,
		IsStaleEmbedding: true,
		DateCreated:      time.Now(),
		DateUpdated:      time.Now(),
	})
	if err != nil {
		return fmt.Errorf("create search sync: %w", err)
	}

	// Create tags
	if err := createTags(ctx, catalogStore, spu.ID, input); err != nil {
		slog.Warn("failed to create tags", slog.Any("error", err))
	}

	// Create promotions
	if err := createPromotionsFromVouchers(ctx, input.Vouchers, spu.ID, accountID, promotionStore); err != nil {
		slog.Warn("failed to create promotions", slog.Any("error", err))
	}

	// Create comments
	if err := createComments(ctx, fake, catalogStore, accountStore, spu.ID, input, accounts); err != nil {
		slog.Warn("failed to create comments", slog.Any("error", err))
	}

	return nil
}
