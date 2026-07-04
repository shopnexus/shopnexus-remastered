package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/gosimple/slug"
	null "github.com/guregu/null/v6"

	catalogdb "shopnexus-server/internal/module/catalog/db/sqlc"
)

// seedCategory represents a category entry in categories.json.
type seedCategory struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// categoryIndex maps lowercased keywords from category names to their UUID.
// Used for fuzzy matching product breadcrumbs to base categories.
type categoryIndex struct {
	byName   map[string]uuid.UUID // exact lowercase name → UUID
	byWord   map[string]uuid.UUID // individual word → UUID (last match wins; used as fallback)
	fallback uuid.UUID            // "General" category
}

func newCategoryIndex() *categoryIndex {
	return &categoryIndex{
		byName: make(map[string]uuid.UUID),
		byWord: make(map[string]uuid.UUID),
	}
}

func (ci *categoryIndex) add(name string, id uuid.UUID) {
	lower := strings.ToLower(name)
	ci.byName[lower] = id

	for _, word := range strings.FieldsFunc(lower, func(r rune) bool {
		return r == ' ' || r == '&' || r == ',' || r == '-' || r == '/'
	}) {
		if len(word) > 2 { // skip tiny words like "of", "a", "&"
			ci.byWord[word] = id
		}
	}
}

// match tries to find the best category for a product breadcrumb.
// Priority: exact name match on any crumb → word match from leaf → fallback.
func (ci *categoryIndex) match(breadcrumb []string) uuid.UUID {
	// 1. Try exact match on each breadcrumb level (prefer deepest match)
	for i := len(breadcrumb) - 1; i >= 0; i-- {
		lower := strings.ToLower(breadcrumb[i])
		if id, ok := ci.byName[lower]; ok {
			return id
		}
	}

	// 2. Try word-level matching from leaf backwards; pick first hit
	for i := len(breadcrumb) - 1; i >= 0; i-- {
		words := strings.FieldsFunc(strings.ToLower(breadcrumb[i]), func(r rune) bool {
			return r == ' ' || r == '&' || r == ',' || r == '-' || r == '/'
		})
		for _, w := range words {
			if id, ok := ci.byWord[w]; ok {
				return id
			}
		}
	}

	return ci.fallback
}

// seedCategories loads categories.json and inserts them into the database.
// Returns a categoryIndex for matching products to categories.
func seedCategories(ctx context.Context, store *catalogdb.Queries) (*categoryIndex, error) {
	data, err := os.ReadFile("./cmd/seed/categories.json")
	if err != nil {
		return nil, fmt.Errorf("read categories.json: %w", err)
	}

	var categories []seedCategory
	if err := json.Unmarshal(data, &categories); err != nil {
		return nil, fmt.Errorf("parse categories.json: %w", err)
	}

	idx := newCategoryIndex()

	for _, cat := range categories {
		var categoryID uuid.UUID

		// Check if already exists by name
		existing, err := store.GetCategory(ctx, catalogdb.GetCategoryParams{
			Name: null.StringFrom(cat.Name),
		})
		if err == nil {
			categoryID = existing.ID
			idx.add(cat.Name, categoryID)
			slog.Info("  Category exists", slog.String("name", cat.Name), slog.String("id", categoryID.String()))
		} else {
			created, err := store.CreateDefaultCategory(ctx, catalogdb.CreateDefaultCategoryParams{
				Name:        cat.Name,
				Description: cat.Description,
				ParentID:    uuid.NullUUID{},
			})
			if err != nil {
				return nil, fmt.Errorf("create category %q: %w", cat.Name, err)
			}
			categoryID = created.ID
			idx.add(cat.Name, categoryID)
			slog.Info("  Created category", slog.String("name", cat.Name), slog.String("id", categoryID.String()))
		}

		// Ensure search_sync row for this category
		if _, err := store.CreateDefaultSearchSync(ctx, catalogdb.CreateDefaultSearchSyncParams{
			RefType: catalogdb.CatalogSearchSyncRefTypeCategory,
			RefID:   categoryID,
		}); err != nil {
			return nil, fmt.Errorf("create search_sync for category %q: %w", cat.Name, err)
		}
	}

	// Ensure a "General" fallback category exists
	general, err := store.GetCategory(ctx, catalogdb.GetCategoryParams{
		Name: null.StringFrom("General"),
	})
	if err != nil {
		general, err = store.CreateDefaultCategory(ctx, catalogdb.CreateDefaultCategoryParams{
			Name:        "General",
			Description: "Uncategorized products",
			ParentID:    uuid.NullUUID{},
		})
		if err != nil {
			return nil, fmt.Errorf("create General category: %w", err)
		}
		slog.Info("  Created category: General", slog.String("id", general.ID.String()))
	}
	idx.fallback = general.ID
	idx.add("General", general.ID)
	if _, err := store.CreateDefaultSearchSync(ctx, catalogdb.CreateDefaultSearchSyncParams{
		RefType: catalogdb.CatalogSearchSyncRefTypeCategory,
		RefID:   general.ID,
	}); err != nil {
		slog.Warn("create default search sync (General)", slog.Any("error", err))
	}

	slog.Info(fmt.Sprintf("Seeded %d categories (+1 General fallback)", len(categories)))
	return idx, nil
}

func createTags(
	ctx context.Context,
	store *catalogdb.Queries,
	spuID uuid.UUID,
	input InputProduct,
) error {
	tagSet := make(map[string]bool)

	for _, crumb := range input.Breadcrumb {
		tagID := slug.Make(crumb)
		if tagID != "" && len(tagID) <= 100 {
			tagSet[tagID] = true
		}
	}

	if input.Brand != "" {
		tagID := slug.Make(input.Brand)
		if tagID != "" && len(tagID) <= 100 {
			tagSet[tagID] = true
		}
	}

	tagSpecs := []string{"Estilo", "Material", "Temporada", "Style", "Season"}
	for _, spec := range input.ProductSpecifications {
		for _, tagSpec := range tagSpecs {
			if spec.Name == tagSpec && spec.Value != "" {
				tagID := slug.Make(spec.Value)
				if tagID != "" && len(tagID) <= 100 {
					tagSet[tagID] = true
				}
			}
		}
	}

	for tagID := range tagSet {
		_, err := store.GetTag(ctx, null.StringFrom(tagID))
		if err != nil {
			_, err = store.CreateTag(ctx, catalogdb.CreateTagParams{
				ID:          tagID,
				Description: null.StringFrom(tagID),
			})
			if err != nil {
				continue
			}

			// Create search_sync row for this tag (deterministic UUID from tag slug)
			tagUUID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(tagID))
			if _, err := store.CreateDefaultSearchSync(ctx, catalogdb.CreateDefaultSearchSyncParams{
				RefType: catalogdb.CatalogSearchSyncRefTypeTag,
				RefID:   tagUUID,
			}); err != nil {
				slog.Warn("create default search sync (tag)", slog.Any("error", err))
			}
		}

		_, err = store.CreateProductSpuTag(ctx, catalogdb.CreateProductSpuTagParams{
			SpuID: spuID,
			Tag:   tagID,
		})
		if err != nil {
			continue
		}
	}

	return nil
}

// generateVariationCombinations generates all combinations of variation options.
func generateVariationCombinations(variations []Variation) [][]map[string]string {
	if len(variations) == 0 {
		return nil
	}

	var validVariations []Variation
	for _, v := range variations {
		if len(v.Variations) > 0 && v.Name != "" {
			validVariations = append(validVariations, v)
		}
	}

	if len(validVariations) == 0 {
		return nil
	}

	total := 1
	for _, v := range validVariations {
		total *= len(v.Variations)
	}

	if total > 20 {
		for i := range validVariations {
			if len(validVariations[i].Variations) > 2 {
				validVariations[i].Variations = validVariations[i].Variations[:2]
			}
		}
	}

	var result [][]map[string]string
	var generate func(depth int, current []map[string]string)
	generate = func(depth int, current []map[string]string) {
		if depth == len(validVariations) {
			combo := make([]map[string]string, len(current))
			copy(combo, current)
			result = append(result, combo)
			return
		}

		v := validVariations[depth]
		for _, opt := range v.Variations {
			attr := map[string]string{
				"name":  v.Name,
				"value": opt,
			}
			generate(depth+1, append(current, attr))
		}
	}

	generate(0, nil)
	return result
}

var stockRegex = regexp.MustCompile(`(?i)existencias|stock`)

func pickCurrentStock(input InputProduct) int64 {
	if input.Stock != nil {
		switch v := input.Stock.(type) {
		case float64:
			return int64(v)
		case string:
			return toBigInt(v)
		default:
			return 0
		}
	}

	for _, spec := range input.ProductSpecifications {
		if stockRegex.MatchString(spec.Name) {
			return toBigInt(spec.Value)
		}
	}

	return 0
}

func toBigInt(value any) int64 {
	if value == nil {
		return 0
	}

	var str string
	switch v := value.(type) {
	case string:
		str = v
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	default:
		str = fmt.Sprintf("%v", v)
	}

	re := regexp.MustCompile(`[^0-9.-]`)
	str = re.ReplaceAllString(str, "")

	num, err := strconv.ParseFloat(str, 64)
	if err != nil {
		return 0
	}

	return int64(num)
}
