package seed

import (
	"context"
	"fmt"

	"shopnexus-remastered/internal/db"

	"github.com/jaswdr/faker/v2"
)

// SharedSeedData holds seeded shared data for other seeders to reference
type SharedSeedData struct {
	Resources []db.SharedResource
}

// SeedSharedSchema seeds the shared schema with fake data
func SeedSharedSchema(ctx context.Context, storage db.Querier, fake *faker.Faker, cfg *SeedConfig) (*SharedSeedData, error) {
	fmt.Println("🗂️ Seeding shared schema...")

	data := &SharedSeedData{
		Resources: make([]db.SharedResource, 0),
	}

	resourceTypes := db.AllSharedResourceTypeValues()
	mimeTypes := []string{
		"image/jpeg", "image/png", "image/gif", "image/webp",
		"application/pdf", "text/plain", "application/json",
	}

	// Create resources
	resourceCount := cfg.AccountCount + cfg.ProductCount // Resources for avatars and product images
	for i := 0; i < resourceCount; i++ {
		resourceType := resourceTypes[fake.RandomDigit()%len(resourceTypes)]
		mimeType := mimeTypes[fake.RandomDigit()%len(mimeTypes)]

		// Ensure image mime types for image-related resources
		if resourceType == "Avatar" || resourceType == "ProductImage" || resourceType == "BrandLogo" {
			imageMimeTypes := []string{"image/jpeg", "image/png", "image/gif", "image/webp"}
			mimeType = imageMimeTypes[fake.RandomDigit()%len(imageMimeTypes)]
		}

		ownerID := int64(fake.RandomDigit()%1000 + 1) // Random owner ID
		order := fake.RandomDigit() % 10              // Order for multiple resources of same owner

		resource, err := retryWithUniqueValues(3, func(attempt int) (db.SharedResource, error) {
			return storage.CreateResource(ctx, db.CreateResourceParams{
				MimeType:  mimeType,
				OwnerID:   ownerID,
				OwnerType: resourceType,
				Url:       generateResourceURL(fake, resourceType, mimeType),
				Order:     int32(order),
			})
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create resource %d: %w", i+1, err)
		}
		data.Resources = append(data.Resources, resource)
	}

	fmt.Printf("✅ Shared schema seeded: %d resources\n", len(data.Resources))
	return data, nil
}

// generateResourceURL creates realistic resource URLs
func generateResourceURL(fake *faker.Faker, resourceType db.SharedResourceType, mimeType string) string {
	domain := "https://storage.shopnexus.com"

	switch resourceType {
	case "Avatar":
		return fmt.Sprintf("%s/avatars/%s.%s", domain, fake.UUID().V4(), getFileExtension(mimeType))
	case "ProductImage":
		return fmt.Sprintf("%s/products/%s.%s", domain, fake.UUID().V4(), getFileExtension(mimeType))
	case "BrandLogo":
		return fmt.Sprintf("%s/brands/%s.%s", domain, fake.UUID().V4(), getFileExtension(mimeType))
	case "Refund":
		return fmt.Sprintf("%s/refunds/%s.%s", domain, fake.UUID().V4(), getFileExtension(mimeType))
	case "ReturnDispute":
		return fmt.Sprintf("%s/disputes/%s.%s", domain, fake.UUID().V4(), getFileExtension(mimeType))
	default:
		return fmt.Sprintf("%s/misc/%s.%s", domain, fake.UUID().V4(), getFileExtension(mimeType))
	}
}

// getFileExtension returns file extension based on mime type
func getFileExtension(mimeType string) string {
	extensions := map[string]string{
		"image/jpeg":       "jpg",
		"image/png":        "png",
		"image/gif":        "gif",
		"image/webp":       "webp",
		"application/pdf":  "pdf",
		"text/plain":       "txt",
		"application/json": "json",
	}

	if ext, exists := extensions[mimeType]; exists {
		return ext
	}
	return "bin" // Default binary extension
}
