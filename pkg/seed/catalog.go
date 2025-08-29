package seed

import (
	"context"
	"fmt"
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
	for _, brandName := range brandNames {
		brand, err := retryWithUniqueValues(3, func(attempt int) (db.CatalogBrand, error) {
			return storage.CreateBrand(ctx, db.CreateBrandParams{
				Code:        generateUniqueCode(fake, "BRAND"),
				Name:        brandName,
				Description: fake.Lorem().Sentence(10),
			})
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create brand %s: %w", brandName, err)
		}
		data.Brands = append(data.Brands, brand)
	}

	// Create categories
	categoryNames := []string{"Electronics", "Clothing", "Sports", "Books", "Home & Garden", "Toys", "Automotive", "Health", "Beauty", "Food & Beverages"}
	for _, categoryName := range categoryNames {
		category, err := storage.CreateCategory(ctx, db.CreateCategoryParams{
			Name:        categoryName,
			Description: fake.Lorem().Sentence(8),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create category %s: %w", categoryName, err)
		}
		data.Categories = append(data.Categories, category)
	}

	// Create subcategories
	subCategories := map[string][]string{
		"Electronics": {"Smartphones", "Laptops", "Tablets", "Cameras", "Headphones"},
		"Clothing":    {"T-Shirts", "Jeans", "Dresses", "Shoes", "Accessories"},
		"Sports":      {"Fitness", "Outdoor", "Team Sports", "Water Sports", "Winter Sports"},
	}

	for parentName, subCats := range subCategories {
		var parentID int64
		for _, cat := range data.Categories {
			if cat.Name == parentName {
				parentID = cat.ID
				break
			}
		}

		for _, subCatName := range subCats {
			subCategory, err := storage.CreateCategory(ctx, db.CreateCategoryParams{
				Name:        subCatName,
				Description: fake.Lorem().Sentence(6),
				ParentID:    pgtype.Int8{Int64: parentID, Valid: parentID != 0},
			})
			if err != nil {
				return nil, fmt.Errorf("failed to create subcategory %s: %w", subCatName, err)
			}
			data.Categories = append(data.Categories, subCategory)
		}
	}

	// Create tags
	tagNames := []string{"new", "popular", "bestseller", "premium", "eco-friendly", "limited-edition", "sale", "trending", "featured", "recommended"}
	for _, tagName := range tagNames {
		tag, err := storage.CreateTag(ctx, db.CreateTagParams{
			Tag:         tagName,
			Description: fake.Lorem().Sentence(5),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create tag %s: %w", tagName, err)
		}
		data.Tags = append(data.Tags, tag)
	}

	// Create product SPUs (only vendors can create products)
	if len(accountData.Vendors) == 0 {
		return data, fmt.Errorf("no vendors available to create products")
	}

	for i := 0; i < cfg.ProductCount; i++ {
		vendor := accountData.Vendors[fake.RandomDigit()%len(accountData.Vendors)]
		category := data.Categories[fake.RandomDigit()%len(data.Categories)]
		brand := data.Brands[fake.RandomDigit()%len(data.Brands)]

		manufactureDate := fake.Time().TimeBetween(
			time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
			time.Now().AddDate(0, -1, 0),
		)

		productName := generateProductName(fake, brand.Name, category.Name)

		spu, err := retryWithUniqueValues(3, func(attempt int) (db.CatalogProductSpu, error) {
			return storage.CreateProductSpu(ctx, db.CreateProductSpuParams{
				Code:             generateUniqueCode(fake, "SPU"),
				AccountID:        vendor.ID,
				CategoryID:       category.ID,
				BrandID:          brand.ID,
				Name:             productName,
				Description:      fake.Lorem().Paragraph(3),
				IsActive:         fake.Boolean().Bool(),
				DateManufactured: pgtype.Timestamptz{Time: manufactureDate, Valid: true},
				DateCreated:      pgtype.Timestamptz{Time: time.Now(), Valid: true},
				DateUpdated:      pgtype.Timestamptz{Time: time.Now(), Valid: true},
			})
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create product SPU %d: %w", i+1, err)
		}
		data.ProductSpus = append(data.ProductSpus, spu)

		// Add 1-3 tags to each product
		tagCount := fake.RandomDigit()%3 + 1
		usedTags := make(map[int64]bool)
		for j := 0; j < tagCount; j++ {
			tag := data.Tags[fake.RandomDigit()%len(data.Tags)]
			if !usedTags[tag.ID] {
				productTag, err := storage.CreateProductSpuTag(ctx, db.CreateProductSpuTagParams{
					SpuID: spu.ID,
					TagID: tag.ID,
				})
				if err != nil {
					return nil, fmt.Errorf("failed to create product tag: %w", err)
				}
				data.ProductTags = append(data.ProductTags, productTag)
				usedTags[tag.ID] = true
			}
		}

		// Create 1-5 SKUs for each SPU
		skuCount := fake.RandomDigit()%5 + 1
		for j := 0; j < skuCount; j++ {
			price := int64(fake.RandomFloat(2, 10, 5000) * 100) // Convert to cents

			sku, err := retryWithUniqueValues(3, func(attempt int) (db.CatalogProductSku, error) {
				return storage.CreateProductSku(ctx, db.CreateProductSkuParams{
					Code:        generateUniqueCode(fake, "SKU"),
					SpuID:       spu.ID,
					Price:       price,
					CanCombine:  fake.Boolean().Bool(),
					DateCreated: pgtype.Timestamptz{Time: time.Now(), Valid: true},
					DateDeleted: pgtype.Timestamptz{Time: time.Time{}, Valid: false},
				})
			})
			if err != nil {
				return nil, fmt.Errorf("failed to create product SKU: %w", err)
			}
			data.ProductSkus = append(data.ProductSkus, sku)

			// Add attributes to SKU (size, color, etc.)
			attributes := generateSkuAttributes(fake, category.Name)
			for attrName, attrValue := range attributes {
				skuAttr, err := retryWithUniqueValues(3, func(attempt int) (db.CatalogProductSkuAttribute, error) {
					return storage.CreateProductSkuAttribute(ctx, db.CreateProductSkuAttributeParams{
						Code:        generateUniqueCode(fake, "ATTR"),
						SkuID:       sku.ID,
						Name:        attrName,
						Value:       attrValue,
						DateUpdated: pgtype.Timestamptz{Time: time.Now(), Valid: true},
						DateCreated: pgtype.Timestamptz{Time: time.Now(), Valid: true},
					})
				})
				if err != nil {
					return nil, fmt.Errorf("failed to create SKU attribute: %w", err)
				}
				data.SkuAttributes = append(data.SkuAttributes, skuAttr)
			}
		}
	}

	//// Create comments on products (only customers can comment)
	//if len(accountData.Customers) > 0 && len(data.ProductSpus) > 0 {
	//	for i := 0; i < cfg.CommentCount; i++ {
	//		customer := accountData.Customers[fake.RandomDigit()%len(accountData.Customers)]
	//		product := data.ProductSpus[fake.RandomDigit()%len(data.ProductSpus)]
	//
	//		comment, err := retryWithUniqueValues(3, func(attempt int) (db.CatalogComment, error) {
	//			return storage.CreateComment(ctx, db.CreateCommentParams{
	//				Code:        generateUniqueCode(fake, "COMMENT"),
	//				AccountID:   customer.ID,
	//				RefType:     db.CatalogCommentDestTypeProductSPU,
	//				RefID:       product.ID,
	//				Body:        fake.Lorem().Paragraph(2),
	//				Upvote:      int64(fake.RandomDigit() % 100),
	//				Downvote:    int64(fake.RandomDigit() % 50),
	//				Score:       int32(fake.RandomDigit()%5 + 1), // 1-5 stars
	//				DateCreated: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	//				DateUpdated: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	//			})
	//		})
	//		if err != nil {
	//			return nil, fmt.Errorf("failed to create comment: %w", err)
	//		}
	//		data.Comments = append(data.Comments, comment)
	//	}
	//}

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
