package seed

import (
	"context"
	"fmt"
	"shopnexus-remastered/internal/utils/pgutil"
	"strings"
	"time"

	"shopnexus-remastered/internal/db"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jaswdr/faker/v2"
)

// CatalogSeedData holds seeded catalog data for other seeders to reference
type CatalogSeedData struct {
	Brands        []db.CatalogBrand
	Categories    []db.CatalogCategory
	ProductSpus   []db.CatalogProductSpu
	ProductSkus   []db.CatalogProductSku
	SkuAttributes []db.CatalogProductSkuAttribute
	Tags          []db.CatalogTag
	ProductTags   []db.CatalogProductSpuTag
	Comments      []db.CatalogComment
}

// SeedCatalogSchema seeds the catalog schema with fake data
func SeedCatalogSchema(ctx context.Context, storage db.Querier, fake *faker.Faker, cfg *SeedConfig, accountData *AccountSeedData) (*CatalogSeedData, error) {
	fmt.Println("🛍️ Seeding catalog schema...")

	// Tạo unique tracker để theo dõi tính duy nhất
	tracker := NewUniqueTracker()

	data := &CatalogSeedData{
		Brands:        make([]db.CatalogBrand, 0),
		Categories:    make([]db.CatalogCategory, 0),
		ProductSpus:   make([]db.CatalogProductSpu, 0),
		ProductSkus:   make([]db.CatalogProductSku, 0),
		SkuAttributes: make([]db.CatalogProductSkuAttribute, 0),
		Tags:          make([]db.CatalogTag, 0),
		ProductTags:   make([]db.CatalogProductSpuTag, 0),
		Comments:      make([]db.CatalogComment, 0),
	}

	// Create brands
	brandNames := []string{"Apple", "Samsung", "Nike", "Adidas", "Sony", "LG", "Canon", "Nikon", "Dell", "HP", "Asus", "MSI", "Razer", "Logitech", "Microsoft"}
	brandParams := make([]db.CreateCatalogBrandParams, len(brandNames))
	for i, brandName := range brandNames {
		brandParams[i] = db.CreateCatalogBrandParams{
			Code:        generateUniqueCodeWithTracker(fake, "BRAND", tracker),
			Name:        brandName,
			Description: fake.Lorem().Sentence(10),
		}
	}

	// Bulk insert brands
	_, err := storage.CreateCatalogBrand(ctx, brandParams)
	if err != nil {
		return nil, fmt.Errorf("failed to bulk create brands: %w", err)
	}

	// Query back created brands
	brands, err := storage.ListCatalogBrand(ctx, db.ListCatalogBrandParams{
		Limit:  pgutil.Int32ToPgInt4(int32(len(brandParams) * 2)),
		Offset: pgutil.Int32ToPgInt4(0),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query back created brands: %w", err)
	}

	// Match brands with our parameters by code (unique identifier)
	brandCodeMap := make(map[string]db.CatalogBrand)
	for _, brand := range brands {
		brandCodeMap[brand.Code] = brand
	}

	// Populate data.Brands with actual database records
	for _, params := range brandParams {
		if brand, exists := brandCodeMap[params.Code]; exists {
			data.Brands = append(data.Brands, brand)
		}
	}

	// Create categories
	categoryNames := []string{"Electronics", "Clothing", "Sports", "Books", "Home & Garden", "Toys", "Automotive", "Health", "Beauty", "Food & Beverages"}
	categoryParams := make([]db.CreateCatalogCategoryParams, len(categoryNames))
	for i, categoryName := range categoryNames {
		categoryParams[i] = db.CreateCatalogCategoryParams{
			Name:        categoryName,
			Description: fake.Lorem().Sentence(8),
		}
	}

	// Bulk insert main categories
	_, err = storage.CreateCatalogCategory(ctx, categoryParams)
	if err != nil {
		return nil, fmt.Errorf("failed to bulk create categories: %w", err)
	}

	// Query back created main categories
	mainCategories, err := storage.ListCatalogCategory(ctx, db.ListCatalogCategoryParams{
		Limit:  pgutil.Int32ToPgInt4(int32(len(categoryParams) * 2)),
		Offset: pgutil.Int32ToPgInt4(0),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query back created main categories: %w", err)
	}

	// Match main categories with our parameters by name
	mainCategoryNameMap := make(map[string]db.CatalogCategory)
	for _, category := range mainCategories {
		if !category.ParentID.Valid { // Only main categories (no parent)
			mainCategoryNameMap[category.Name] = category
		}
	}

	// Populate data.Categories with main categories first
	for _, params := range categoryParams {
		if category, exists := mainCategoryNameMap[params.Name]; exists {
			data.Categories = append(data.Categories, category)
		}
	}

	// Create subcategories
	subCategories := map[string][]string{
		"Electronics": {"Smartphones", "Laptops", "Tablets", "Cameras", "Headphones"},
		"Clothing":    {"T-Shirts", "Jeans", "Dresses", "Shoes", "Accessories"},
		"Sports":      {"Fitness", "Outdoor", "Team Sports", "Water Sports", "Winter Sports"},
	}

	var subCategoryParams []db.CreateCatalogCategoryParams
	for parentName, subCats := range subCategories {
		var parentID int64
		for _, cat := range data.Categories {
			if cat.Name == parentName {
				parentID = cat.ID
				break
			}
		}

		for _, subCatName := range subCats {
			subCategoryParams = append(subCategoryParams, db.CreateCatalogCategoryParams{
				Name:        subCatName,
				Description: fake.Lorem().Sentence(6),
				ParentID:    pgtype.Int8{Int64: parentID, Valid: parentID != 0},
			})
		}
	}

	// Bulk insert subcategories
	if len(subCategoryParams) > 0 {
		_, err = storage.CreateCatalogCategory(ctx, subCategoryParams)
		if err != nil {
			return nil, fmt.Errorf("failed to bulk create subcategories: %w", err)
		}

		// Query back all categories again to get subcategories
		allCategories, err := storage.ListCatalogCategory(ctx, db.ListCatalogCategoryParams{
			Limit:  pgutil.Int32ToPgInt4(int32((len(categoryParams) + len(subCategoryParams)) * 2)),
			Offset: pgutil.Int32ToPgInt4(0),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to query back all categories: %w", err)
		}

		// Match subcategories with our parameters by name and parent
		subCategoryNameMap := make(map[string]db.CatalogCategory)
		for _, category := range allCategories {
			if category.ParentID.Valid { // Only subcategories (have parent)
				subCategoryNameMap[category.Name] = category
			}
		}

		// Add subcategories to data.Categories
		for _, params := range subCategoryParams {
			if category, exists := subCategoryNameMap[params.Name]; exists {
				data.Categories = append(data.Categories, category)
			}
		}
	}

	// Create tags
	tagNames := []string{"new", "popular", "bestseller", "premium", "eco-friendly", "limited-edition", "sale", "trending", "featured", "recommended"}
	tagParams := make([]db.CreateCatalogTagParams, len(tagNames))
	for i, tagName := range tagNames {
		tagParams[i] = db.CreateCatalogTagParams{
			Tag:         tagName,
			Description: fake.Lorem().Sentence(5),
		}
	}

	// Bulk insert tags
	_, err = storage.CreateCatalogTag(ctx, tagParams)
	if err != nil {
		return nil, fmt.Errorf("failed to bulk create tags: %w", err)
	}

	// Query back created tags
	tags, err := storage.ListCatalogTag(ctx, db.ListCatalogTagParams{
		Limit:  pgutil.Int32ToPgInt4(int32(len(tagParams) * 2)),
		Offset: pgutil.Int32ToPgInt4(0),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query back created tags: %w", err)
	}

	// Match tags with our parameters by tag name
	tagNameMap := make(map[string]db.CatalogTag)
	for _, tag := range tags {
		tagNameMap[tag.Tag] = tag
	}

	// Populate data.Tags with actual database records
	for _, params := range tagParams {
		if tag, exists := tagNameMap[params.Tag]; exists {
			data.Tags = append(data.Tags, tag)
		}
	}

	// Create product SPUs (only vendors can create products)
	if len(accountData.Vendors) == 0 {
		return data, fmt.Errorf("no vendors available to create products")
	}

	// Prepare bulk SPU data
	spuParams := make([]db.CreateCatalogProductSpuParams, cfg.ProductCount)
	for i := 0; i < cfg.ProductCount; i++ {
		vendor := accountData.Vendors[fake.RandomDigit()%len(accountData.Vendors)]
		category := data.Categories[fake.RandomDigit()%len(data.Categories)]
		brand := data.Brands[fake.RandomDigit()%len(data.Brands)]

		manufactureDate := fake.Time().TimeBetween(
			time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
			time.Now().AddDate(0, -1, 0),
		)

		productName := generateProductName(fake, brand.Name, category.Name)

		spuParams[i] = db.CreateCatalogProductSpuParams{
			Code:             generateUniqueCodeWithTracker(fake, "SPU", tracker),
			AccountID:        vendor.ID,
			CategoryID:       category.ID,
			BrandID:          brand.ID,
			Name:             productName,
			Description:      fake.Lorem().Paragraph(3),
			IsActive:         fake.Boolean().Bool(),
			DateManufactured: pgtype.Timestamptz{Time: manufactureDate, Valid: true},
			DateCreated:      pgtype.Timestamptz{Time: time.Now(), Valid: true},
			DateUpdated:      pgtype.Timestamptz{Time: time.Now(), Valid: true},
		}
	}

	// Bulk insert SPUs
	_, err = storage.CreateCatalogProductSpu(ctx, spuParams)
	if err != nil {
		return nil, fmt.Errorf("failed to bulk create product SPUs: %w", err)
	}

	// Query back created SPUs
	spus, err := storage.ListCatalogProductSpu(ctx, db.ListCatalogProductSpuParams{
		Limit:  pgutil.Int32ToPgInt4(int32(len(spuParams) * 2)),
		Offset: pgutil.Int32ToPgInt4(0),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query back created SPUs: %w", err)
	}

	// Match SPUs with our parameters by code (unique identifier)
	spuCodeMap := make(map[string]db.CatalogProductSpu)
	for _, spu := range spus {
		spuCodeMap[spu.Code] = spu
	}

	// Populate data.ProductSpus with actual database records
	for _, params := range spuParams {
		if spu, exists := spuCodeMap[params.Code]; exists {
			data.ProductSpus = append(data.ProductSpus, spu)
		}
	}

	// Prepare bulk product tag data
	var productTagParams []db.CreateCatalogProductSpuTagParams
	for _, spu := range data.ProductSpus {
		tagCount := fake.RandomDigit()%3 + 1
		usedTags := make(map[int64]bool)
		for j := 0; j < tagCount; j++ {
			tag := data.Tags[fake.RandomDigit()%len(data.Tags)]
			if !usedTags[tag.ID] {
				productTagParams = append(productTagParams, db.CreateCatalogProductSpuTagParams{
					SpuID: spu.ID,
					TagID: tag.ID,
				})
				usedTags[tag.ID] = true
			}
		}
	}

	// Bulk insert product tags
	if len(productTagParams) > 0 {
		_, err = storage.CreateCatalogProductSpuTag(ctx, productTagParams)
		if err != nil {
			return nil, fmt.Errorf("failed to bulk create product tags: %w", err)
		}

		// Query back created product tags
		productTags, err := storage.ListCatalogProductSpuTag(ctx, db.ListCatalogProductSpuTagParams{
			Limit:  pgutil.Int32ToPgInt4(int32(len(productTagParams) * 2)),
			Offset: pgutil.Int32ToPgInt4(0),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to query back created product tags: %w", err)
		}

		// Populate data.ProductTags with actual database records
		data.ProductTags = productTags
	}

	// Prepare bulk SKU data
	var skuParams []db.CreateCatalogProductSkuParams
	var skuAttributeParams []db.CreateCatalogProductSkuAttributeParams

	for _, spu := range data.ProductSpus {
		skuCount := fake.RandomDigit()%5 + 1
		for j := 0; j < skuCount; j++ {
			price := int64(fake.RandomFloat(2, 10, 5000) * 100) // Convert to cents

			skuCode := generateUniqueCodeWithTracker(fake, "SKU", tracker)
			skuParams = append(skuParams, db.CreateCatalogProductSkuParams{
				Code:        skuCode,
				SpuID:       spu.ID,
				Price:       price,
				CanCombine:  fake.Boolean().Bool(),
				DateCreated: pgtype.Timestamptz{Time: time.Now(), Valid: true},
				DateDeleted: pgtype.Timestamptz{Time: time.Time{}, Valid: false},
			})

			// Find category for this SPU to generate appropriate attributes
			var categoryName string
			for _, cat := range data.Categories {
				if cat.ID == spu.CategoryID {
					categoryName = cat.Name
					break
				}
			}

			// Store attributes info to be created later (after we get SKU IDs)
			// We'll create a temporary mapping structure
			attributes := generateSkuAttributes(fake, categoryName)
			for attrName, attrValue := range attributes {
				// We'll store the SKU code as a comment in the attribute code to match later
				attrCode := fmt.Sprintf("%s_%s", generateUniqueCodeWithTracker(fake, "ATTR", tracker), skuCode)
				skuAttributeParams = append(skuAttributeParams, db.CreateCatalogProductSkuAttributeParams{
					Code:        attrCode,
					SkuID:       0, // Will be filled after SKU creation
					Name:        attrName,
					Value:       attrValue,
					DateUpdated: pgtype.Timestamptz{Time: time.Now(), Valid: true},
					DateCreated: pgtype.Timestamptz{Time: time.Now(), Valid: true},
				})
			}
		}
	}

	// Bulk insert SKUs
	if len(skuParams) > 0 {
		_, err = storage.CreateCatalogProductSku(ctx, skuParams)
		if err != nil {
			return nil, fmt.Errorf("failed to bulk create product SKUs: %w", err)
		}

		// Query back created SKUs
		skus, err := storage.ListCatalogProductSku(ctx, db.ListCatalogProductSkuParams{
			Limit:  pgutil.Int32ToPgInt4(int32(len(skuParams) * 2)),
			Offset: pgutil.Int32ToPgInt4(0),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to query back created SKUs: %w", err)
		}

		// Match SKUs with our parameters by code (unique identifier)
		skuCodeMap := make(map[string]db.CatalogProductSku)
		for _, sku := range skus {
			skuCodeMap[sku.Code] = sku
		}

		// Populate data.ProductSkus with actual database records
		for _, params := range skuParams {
			if sku, exists := skuCodeMap[params.Code]; exists {
				data.ProductSkus = append(data.ProductSkus, sku)
			}
		}

		// Now update SKU attributes with actual SKU IDs
		for i := range skuAttributeParams {
			// Extract SKU code from attribute code (format: ATTR_xxx_SKU_code)
			parts := strings.Split(skuAttributeParams[i].Code, "_")
			if len(parts) >= 4 {
				skuCode := strings.Join(parts[len(parts)-3:], "_") // Get last 3 parts as SKU code
				if sku, exists := skuCodeMap[skuCode]; exists {
					skuAttributeParams[i].SkuID = sku.ID
				}
			}
		}
	}

	// Bulk insert SKU attributes
	if len(skuAttributeParams) > 0 {
		// Filter out attributes without valid SKU IDs
		validAttributeParams := make([]db.CreateCatalogProductSkuAttributeParams, 0)
		for _, attr := range skuAttributeParams {
			if attr.SkuID > 0 {
				validAttributeParams = append(validAttributeParams, attr)
			}
		}

		if len(validAttributeParams) > 0 {
			_, err = storage.CreateCatalogProductSkuAttribute(ctx, validAttributeParams)
			if err != nil {
				return nil, fmt.Errorf("failed to bulk create SKU attributes: %w", err)
			}

			// Query back created SKU attributes
			skuAttributes, err := storage.ListCatalogProductSkuAttribute(ctx, db.ListCatalogProductSkuAttributeParams{
				Limit:  pgutil.Int32ToPgInt4(int32(len(validAttributeParams) * 2)),
				Offset: pgutil.Int32ToPgInt4(0),
			})
			if err != nil {
				return nil, fmt.Errorf("failed to query back created SKU attributes: %w", err)
			}

			// Populate data.SkuAttributes with actual database records
			data.SkuAttributes = skuAttributes
		}
	}

	// Create comments for products with business rules:
	// 1. Each account can only comment once per product SPU
	// 2. Only allow 1 nested comment from shop (vendor) per customer comment
	// 3. Simple structure without complex nesting like social media
	if len(accountData.Customers) > 0 && len(data.ProductSpus) > 0 {
		var commentParams []db.CreateCatalogCommentParams
		var vendorCommentParams []db.CreateCatalogCommentParams

		// Create customer comments for products (1 comment per customer per product)
		for _, spu := range data.ProductSpus {
			// Select random customers for this product (max 3 customers per product)
			customerCount := fake.RandomDigit()%3 + 1 // 1-3 customers per product
			selectedCustomers := make(map[int64]bool) // Track selected customers to avoid duplicates

			for j := 0; j < customerCount && len(selectedCustomers) < len(accountData.Customers); j++ {
				var customer db.AccountCustomer
				// Find a customer that hasn't been selected yet
				for {
					customer = accountData.Customers[fake.RandomDigit()%len(accountData.Customers)]
					if !selectedCustomers[customer.ID] {
						selectedCustomers[customer.ID] = true
						break
					}
				}

				commentParams = append(commentParams, db.CreateCatalogCommentParams{
					Code:        generateUniqueCodeWithTracker(fake, "COMMENT", tracker),
					AccountID:   customer.ID,
					RefType:     db.CatalogCommentRefTypeProductSPU,
					RefID:       spu.ID,
					Body:        generateCommentBody(fake),
					Upvote:      int64(fake.RandomDigit() % 50),
					Downvote:    int64(fake.RandomDigit() % 10),
					Score:       int32(fake.RandomDigit() % 101), // 0-100
					DateCreated: pgtype.Timestamptz{Time: time.Now().Add(-time.Duration(fake.RandomDigit()%720) * time.Hour), Valid: true},
					DateUpdated: pgtype.Timestamptz{Time: time.Now(), Valid: true},
				})
			}
		}

		// Bulk insert customer comments first
		if len(commentParams) > 0 {
			_, err = storage.CreateCatalogComment(ctx, commentParams)
			if err != nil {
				return nil, fmt.Errorf("failed to bulk create customer comments: %w", err)
			}

			// Query back created customer comments
			customerComments, err := storage.ListCatalogComment(ctx, db.ListCatalogCommentParams{
				Limit:  pgutil.Int32ToPgInt4(int32(len(commentParams) * 2)),
				Offset: pgutil.Int32ToPgInt4(0),
			})
			if err != nil {
				return nil, fmt.Errorf("failed to query back created customer comments: %w", err)
			}

			// Create vendor replies to some customer comments (only 1 reply per customer comment)
			if len(accountData.Vendors) > 0 && len(customerComments) > 0 {
				replyCount := len(customerComments) / 2 // 50% of customer comments get vendor replies
				for i := 0; i < replyCount && i < len(customerComments); i++ {
					vendor := accountData.Vendors[fake.RandomDigit()%len(accountData.Vendors)]

					vendorCommentParams = append(vendorCommentParams, db.CreateCatalogCommentParams{
						Code:        generateUniqueCodeWithTracker(fake, "COMMENT", tracker),
						AccountID:   vendor.ID,
						RefType:     db.CatalogCommentRefTypeComment,
						RefID:       customerComments[i].ID, // Reference to customer comment
						Body:        generateCommentBody(fake),
						Upvote:      int64(fake.RandomDigit() % 10),
						Downvote:    int64(fake.RandomDigit() % 2),
						Score:       int32(fake.RandomDigit() % 101),
						DateCreated: pgtype.Timestamptz{Time: time.Now().Add(-time.Duration(fake.RandomDigit()%360) * time.Hour), Valid: true},
						DateUpdated: pgtype.Timestamptz{Time: time.Now(), Valid: true},
					})
				}

				// Bulk insert vendor comments
				if len(vendorCommentParams) > 0 {
					_, err = storage.CreateCatalogComment(ctx, vendorCommentParams)
					if err != nil {
						return nil, fmt.Errorf("failed to bulk create vendor comments: %w", err)
					}
				}
			}

			// Query back all comments (customer + vendor)
			allComments, err := storage.ListCatalogComment(ctx, db.ListCatalogCommentParams{
				Limit:  pgutil.Int32ToPgInt4(int32((len(commentParams) + len(vendorCommentParams)) * 2)),
				Offset: pgutil.Int32ToPgInt4(0),
			})
			if err != nil {
				return nil, fmt.Errorf("failed to query back all comments: %w", err)
			}

			// Populate data.Comments with actual database records
			data.Comments = allComments
		}
	}

	fmt.Printf("✅ Catalog schema seeded: %d brands, %d categories, %d SPUs, %d SKUs, %d attributes, %d tags, %d product tags, %d comments\n",
		len(data.Brands), len(data.Categories), len(data.ProductSpus), len(data.ProductSkus),
		len(data.SkuAttributes), len(data.Tags), len(data.ProductTags), len(data.Comments))

	return data, nil
}

// generateProductName creates realistic product names based on brand and category
func generateProductName(fake *faker.Faker, brandName, categoryName string) string {
	productTypes := map[string][]string{
		"Electronics": {"Pro", "Max", "Ultra", "Plus", "Mini", "Air", "Studio"},
		"Smartphones": {"Pro", "Max", "Ultra", "Plus", "Mini", "Lite", "Edge"},
		"Laptops":     {"Book", "Pro", "Gaming", "Ultra", "Slim", "Studio"},
		"Clothing":    {"Classic", "Premium", "Sport", "Casual", "Luxury"},
		"Sports":      {"Pro", "Elite", "Performance", "Training", "Outdoor"},
	}

	var suffix string
	if types, exists := productTypes[categoryName]; exists {
		suffix = types[fake.RandomDigit()%len(types)]
	} else {
		suffix = []string{"Pro", "Max", "Ultra", "Plus", "Classic"}[fake.RandomDigit()%5]
	}

	model := fake.Lorem().Word()
	return fmt.Sprintf("%s %s %s", brandName, model, suffix)
}

// generateSkuAttributes creates realistic attributes based on category
func generateSkuAttributes(fake *faker.Faker, categoryName string) map[string]string {
	attributes := make(map[string]string)

	switch categoryName {
	case "Clothing", "T-Shirts", "Jeans", "Dresses":
		sizes := []string{"XS", "S", "M", "L", "XL", "XXL"}
		colors := []string{"Black", "White", "Blue", "Red", "Green", "Yellow", "Gray", "Navy"}
		attributes["size"] = sizes[fake.RandomDigit()%len(sizes)]
		attributes["color"] = colors[fake.RandomDigit()%len(colors)]
	case "Shoes":
		sizes := []string{"36", "37", "38", "39", "40", "41", "42", "43", "44", "45"}
		colors := []string{"Black", "White", "Brown", "Blue", "Red", "Gray"}
		attributes["size"] = sizes[fake.RandomDigit()%len(sizes)]
		attributes["color"] = colors[fake.RandomDigit()%len(colors)]
	case "Electronics", "Smartphones", "Laptops":
		colors := []string{"Black", "White", "Silver", "Gold", "Blue", "Red"}
		storages := []string{"64GB", "128GB", "256GB", "512GB", "1TB"}
		attributes["color"] = colors[fake.RandomDigit()%len(colors)]
		attributes["storage"] = storages[fake.RandomDigit()%len(storages)]
	default:
		colors := []string{"Black", "White", "Silver", "Blue", "Red", "Green"}
		attributes["color"] = colors[fake.RandomDigit()%len(colors)]
	}

	return attributes
}

// generateCommentBody creates realistic comment content
func generateCommentBody(fake *faker.Faker) string {
	commentTemplates := []string{
		"Great product! %s",
		"I love this %s. Highly recommended!",
		"Good quality but %s",
		"Not bad, %s",
		"Excellent value for money. %s",
		"Could be better. %s",
		"Perfect for my needs. %s",
		"Disappointed with %s",
		"Amazing product! %s",
		"Worth every penny. %s",
	}

	template := commentTemplates[fake.RandomDigit()%len(commentTemplates)]
	details := fake.Lorem().Sentence(3)

	return fmt.Sprintf(template, details)
}
