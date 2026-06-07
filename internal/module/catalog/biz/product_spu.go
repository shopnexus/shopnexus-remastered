package catalogbiz

import (
	"encoding/json"
	"fmt"
	"strings"

	restate "github.com/restatedev/sdk-go"

	"github.com/google/uuid"
	"github.com/gosimple/slug"
	"github.com/samber/lo"

	accountbiz "shopnexus-server/internal/module/account/biz"
	accountmodel "shopnexus-server/internal/module/account/model"
	catalogdb "shopnexus-server/internal/module/catalog/db/sqlc"
	catalogmodel "shopnexus-server/internal/module/catalog/model"
	commonbiz "shopnexus-server/internal/module/common/biz"
	commondb "shopnexus-server/internal/module/common/db/sqlc"
	sharedcurrency "shopnexus-server/internal/shared/currency"
	"shopnexus-server/internal/shared/errors"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/validator"

	"github.com/guregu/null/v6"
)

func (b *CatalogHandler) getTagsMap(ctx restate.Context, spuID []uuid.UUID) map[uuid.UUID][]string { // map[spuID][]tag
	tags, err := b.storage.Querier().ListProductSpuTag(ctx, catalogdb.ListProductSpuTagParams{
		SpuID: spuID,
	})
	if err != nil {
		zero := map[uuid.UUID][]string{}
		for _, id := range spuID {
			zero[id] = []string{}
		}
		return zero
	}
	return lo.GroupByMap(
		tags,
		func(tag catalogdb.CatalogProductSpuTag) (uuid.UUID, string) { return tag.SpuID, tag.Tag },
	)
}

func (b *CatalogHandler) getRatingsMap(
	ctx restate.Context,
	spuIDs []uuid.UUID,
) (map[uuid.UUID]catalogdb.ListRatingRow, error) {
	ratings, err := b.storage.Querier().ListRating(ctx, catalogdb.ListRatingParams{
		RefType: catalogdb.CatalogCommentRefTypeProductSpu,
		RefID:   spuIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("db list rating: %w", err)
	}
	return lo.KeyBy(ratings, func(r catalogdb.ListRatingRow) uuid.UUID { return r.RefID }), nil
}

func (b *CatalogHandler) getCategory(ctx restate.Context, categoryID uuid.UUID) catalogdb.CatalogCategory {
	category, _ := b.storage.Querier().GetCategory(ctx, catalogdb.GetCategoryParams{
		ID: uuid.NullUUID{UUID: categoryID, Valid: true},
	})
	return category
}

// getCategoriesMap batch-fetches categories by IDs and returns a map keyed by category ID.
func (b *CatalogHandler) getCategoriesMap(
	ctx restate.Context,
	categoryIDs []uuid.UUID,
) map[uuid.UUID]catalogdb.CatalogCategory {
	if len(categoryIDs) == 0 {
		return map[uuid.UUID]catalogdb.CatalogCategory{}
	}
	categories, err := b.storage.Querier().ListCategory(ctx, catalogdb.ListCategoryParams{
		ID: lo.Uniq(categoryIDs),
	})
	if err != nil {
		return map[uuid.UUID]catalogdb.CatalogCategory{}
	}
	return lo.KeyBy(categories, func(c catalogdb.CatalogCategory) uuid.UUID { return c.ID })
}

type GetProductSpuParams struct {
	ID   uuid.NullUUID `validate:"omitnil"`
	Slug null.String   `validate:"omitnil"`
}

// GetProductSpu returns a single product SPU by ID or slug.
func (b *CatalogHandler) GetProductSpu(
	ctx restate.Context,
	params GetProductSpuParams,
) (catalogmodel.ProductSpu, error) {
	if err := validator.Validate(params); err != nil {
		return catalogmodel.ProductSpu{}, fmt.Errorf("validate get product spu params: %w", err)
	}

	var (
		listSpu paginate.PaginateResult[catalogmodel.ProductSpu]
		err     error
	)

	if params.ID.Valid {
		listSpu, err = b.ListProductSpu(ctx, ListProductSpuParams{
			ID: []uuid.UUID{params.ID.UUID},
		})
		if err != nil {
			return catalogmodel.ProductSpu{}, fmt.Errorf("get product spu: %w", err)
		}
	} else if params.Slug.Valid {
		listSpu, err = b.ListProductSpu(ctx, ListProductSpuParams{
			Slug: []string{params.Slug.String},
		})
		if err != nil {
			return catalogmodel.ProductSpu{}, fmt.Errorf("get product spu by slug: %w", err)
		}
	}

	if len(listSpu.Data) == 0 {
		return catalogmodel.ProductSpu{}, errors.ErrEntityNotFound.Fmt("ProductSpu")
	}
	return listSpu.Data[0], nil
}

type ListProductSpuParams struct {
	paginate.Params

	Account    accountmodel.AuthenticatedAccount `validate:"omitempty"`
	ID         []uuid.UUID                       `validate:"omitempty,dive"`
	Slug       []string                          `validate:"omitempty,dive"`
	AccountID  []uuid.UUID                       `validate:"omitempty,dive"`
	CategoryID []uuid.UUID                       `validate:"omitempty,dive"`
	IsEnabled  []bool                            `validate:"omitempty,dive"`
	Search     null.String                       `validate:"omitnil"`
}

// ListProductSpu returns paginated product SPUs with optional filters for category and active status.
func (b *CatalogHandler) ListProductSpu(
	ctx restate.Context,
	params ListProductSpuParams,
) (paginate.PaginateResult[catalogmodel.ProductSpu], error) {
	var zero paginate.PaginateResult[catalogmodel.ProductSpu]

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate list product spu: %w", err)
	}

	var dbSpus []catalogdb.CatalogProductSpu
	var total null.Int64

	if params.Search.Valid {
		params.Search.SetValid(strings.TrimSpace(params.Search.String))
		rows, err := b.storage.Querier().SearchCountProductSpu(ctx, catalogdb.SearchCountProductSpuParams{
			Limit:      params.Limit,
			Offset:     params.Offset(),
			ID:         params.ID,
			AccountID:  params.AccountID,
			CategoryID: params.CategoryID,
			IsEnabled:  params.IsEnabled,
			Name:       params.Search,
			Slug:       params.Search,
		})
		if err != nil {
			return zero, fmt.Errorf("db search product spu: %w", err)
		}
		if len(rows) > 0 {
			total.SetValid(rows[0].TotalCount)
		}
		dbSpus = lo.Map(rows, func(row catalogdb.SearchCountProductSpuRow, _ int) catalogdb.CatalogProductSpu {
			return row.CatalogProductSpu
		})
	} else {
		rows, err := b.storage.Querier().ListCountProductSpuRecent(ctx, catalogdb.ListCountProductSpuRecentParams{
			Limit:      params.Limit,
			Offset:     params.Offset(),
			ID:         params.ID,
			Slug:       params.Slug,
			AccountID:  params.AccountID,
			CategoryID: params.CategoryID,
			IsEnabled:  params.IsEnabled,
		})
		if err != nil {
			return zero, fmt.Errorf("db list product spu: %w", err)
		}
		if len(rows) > 0 {
			total.SetValid(rows[0].TotalCount)
		}
		dbSpus = lo.Map(rows, func(row catalogdb.ListCountProductSpuRecentRow, _ int) catalogdb.CatalogProductSpu {
			return row.CatalogProductSpu
		})
	}

	spuIDs := lo.Map(dbSpus, func(spu catalogdb.CatalogProductSpu, _ int) uuid.UUID { return spu.ID })
	categoryIDs := lo.Map(dbSpus, func(spu catalogdb.CatalogProductSpu, _ int) uuid.UUID { return spu.CategoryID })
	categoriesMap := b.getCategoriesMap(ctx, categoryIDs)

	ratingMap, err := b.getRatingsMap(ctx, spuIDs)
	if err != nil {
		return zero, fmt.Errorf("db list rating: %w", err)
	}

	tagsMap := b.getTagsMap(ctx, spuIDs)

	resourcesMap, err := b.common.GetResources(ctx, commonbiz.GetResourcesParams{
		RefType: commondb.CommonResourceRefTypeProductSpu,
		RefIDs:  spuIDs,
	})
	if err != nil {
		return zero, fmt.Errorf("get product resources: %w", err)
	}

	// Fetch search sync status
	syncStatuses, _ := b.storage.Querier().ListSearchSync(ctx, catalogdb.ListSearchSyncParams{
		RefID: spuIDs,
	})
	syncMap := lo.KeyBy(syncStatuses, func(s catalogdb.CatalogSearchSync) uuid.UUID { return s.RefID })

	var spus []catalogmodel.ProductSpu
	for _, spu := range dbSpus {
		specs := []catalogmodel.ProductSpecification{}
		if err := json.Unmarshal(spu.Specifications, &specs); err != nil {
			return zero, fmt.Errorf("unmarshal specifications: %w", err)
		}

		m := b.mapProductSpu(spu, categoriesMap[spu.CategoryID])
		m.Rating = catalogmodel.ProductRating{
			Score: ratingMap[spu.ID].Score,
			Total: ratingMap[spu.ID].Count,
		}
		m.Tags = tagsMap[spu.ID]
		m.Resources = resourcesMap[spu.ID]
		m.Specifications = specs
		if sync, ok := syncMap[spu.ID]; ok {
			m.IsStaleEmbedding = sync.IsStaleEmbedding
		}
		spus = append(spus, m)
	}

	return paginate.PaginateResult[catalogmodel.ProductSpu]{
		PageParams: params.Params,
		Total:      total,
		Data:       spus,
	}, nil
}

type CreateProductSpuParams struct {
	Account        accountmodel.AuthenticatedAccount
	CategoryID     uuid.UUID                           `validate:"required"`
	Name           string                              `validate:"required,min=1,max=200"`
	Description    string                              `validate:"required,max=100000"`
	Currency       string                              `validate:"required,iso4217"`
	IsEnabled      bool                                `validate:"omitempty"`
	Tags           []string                            `validate:"required,dive,min=1,max=100"`
	ResourceIDs    []uuid.UUID                         `validate:"omitempty,dive"`
	Specifications []catalogmodel.ProductSpecification `validate:"omitempty,dive"`
}

// CreateProductSpu creates a new product SPU with tags, resources, and search sync entry.
func (b *CatalogHandler) CreateProductSpu(
	ctx restate.Context,
	params CreateProductSpuParams,
) (catalogmodel.ProductSpu, error) {
	var zero catalogmodel.ProductSpu

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate create product spu: %w", err)
	}

	if err := b.assertSellerCurrency(ctx, params.Account, params.Currency); err != nil {
		return zero, fmt.Errorf("assert seller currency: %w", err)
	}

	specsBytes, err := json.Marshal(params.Specifications)
	if err != nil {
		return zero, fmt.Errorf("create product spu: %w", err)
	}

	spu, err := b.storage.Querier().CreateDefaultProductSpu(ctx, catalogdb.CreateDefaultProductSpuParams{
		Slug:           GenerateSlug(params.Name),
		AccountID:      params.Account.ID,
		CategoryID:     params.CategoryID,
		Name:           params.Name,
		Description:    params.Description,
		IsEnabled:      params.IsEnabled,
		Currency:       params.Currency,
		Specifications: specsBytes,
	})
	if err != nil {
		return zero, fmt.Errorf("db create product spu: %w", err)
	}

	// Create tags
	if err := b.updateTags(ctx, b.storage.Querier(), updateTagsParams{
		SpuID: spu.ID,
		Tags:  params.Tags,
	}); err != nil {
		return zero, fmt.Errorf("create product spu: %w", err)
	}

	// Create resources
	resources, err := b.common.UpdateResources(ctx, commonbiz.UpdateResourcesParams{
		Account:     params.Account,
		RefType:     commondb.CommonResourceRefTypeProductSpu,
		RefID:       spu.ID,
		ResourceIDs: params.ResourceIDs,
	})
	if err != nil {
		return zero, fmt.Errorf("create product spu: %w", err)
	}

	if _, err := b.storage.Querier().CreateDefaultSearchSync(ctx, catalogdb.CreateDefaultSearchSyncParams{
		RefType: catalogdb.CatalogSearchSyncRefTypeProductSpu,
		RefID:   spu.ID,
	}); err != nil {
		return zero, fmt.Errorf("db create search sync: %w", err)
	}

	tagsMap := b.getTagsMap(ctx, []uuid.UUID{spu.ID})

	m := b.mapProductSpu(spu, b.getCategory(ctx, spu.CategoryID))
	m.Tags = tagsMap[spu.ID]
	m.Resources = resources
	m.Specifications = params.Specifications
	return m, nil
}

type UpdateProductSpuParams struct {
	Account        accountmodel.AuthenticatedAccount
	ID             uuid.UUID                           `validate:"required"`
	FeaturedSkuID  uuid.NullUUID                       `validate:"omitnil"`
	CategoryID     uuid.NullUUID                       `validate:"omitnil"`
	Name           null.String                         `validate:"omitnil,min=1,max=200"`
	Description    null.String                         `validate:"omitnil,max=100000"`
	Currency       null.String                         `validate:"omitnil,iso4217"`
	IsEnabled      null.Bool                           `validate:"omitnil"`
	RegenerateSlug bool                                `validate:"omitempty"`
	Tags           []string                            `validate:"omitempty,dive,min=1,max=100"`
	ResourceIDs    []uuid.UUID                         `validate:"omitempty,dive"`
	Specifications []catalogmodel.ProductSpecification `validate:"omitempty,dive"`
}

// UpdateProductSpu updates an existing product SPU and marks the search index as stale.
func (b *CatalogHandler) UpdateProductSpu(
	ctx restate.Context,
	params UpdateProductSpuParams,
) (catalogmodel.ProductSpu, error) {
	var zero catalogmodel.ProductSpu

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate update product spu: %w", err)
	}

	if params.Currency.Valid {
		if err := b.assertSellerCurrency(ctx, params.Account, params.Currency.String); err != nil {
			return zero, fmt.Errorf("assert seller currency: %w", err)
		}
	}

	// Ensure the featured SKU (if provided) belongs to the current SPU.
	if params.FeaturedSkuID.Valid {
		skus, err := b.storage.Querier().ListProductSku(ctx, catalogdb.ListProductSkuParams{
			ID: []uuid.UUID{params.FeaturedSkuID.UUID},
		})
		if err != nil {
			return zero, fmt.Errorf("db validate featured sku: %w", err)
		}
		if len(skus) == 0 || skus[0].SpuID != params.ID {
			return zero, catalogmodel.ErrSkuNotBelongToSpu
		}
	}

	var slug null.String
	if params.RegenerateSlug && params.Name.Valid {
		slug.SetValid(GenerateSlug(params.Name.String))
	}

	specsBytes, err := json.Marshal(params.Specifications)
	if err != nil {
		return zero, fmt.Errorf("update product spu: %w", err)
	}

	// FIRST STEP: Update the product spu
	spu, err := b.storage.Querier().UpdateProductSpu(ctx, catalogdb.UpdateProductSpuParams{
		ID:            params.ID,
		Slug:          slug,
		FeaturedSkuID: params.FeaturedSkuID,
		CategoryID:    params.CategoryID,
		Name:          params.Name,
		Description:   params.Description,
		IsEnabled:     params.IsEnabled,
		Currency:      params.Currency,
		// TODO: auto fill the current_timestampt in pgtempl tool
		// DateUpdated:    time.Now(),
		Specifications: specsBytes,
	})
	if err != nil {
		return zero, fmt.Errorf("db update product spu: %w", err)
	}

	// NEXT STEP: Update tags
	if err := b.updateTags(ctx, b.storage.Querier(), updateTagsParams{
		SpuID: spu.ID,
		Tags:  params.Tags,
	}); err != nil {
		return zero, fmt.Errorf("update product spu: %w", err)
	}

	// NEXT STEP: Mark the search embedding as stale (name/description/tags feed the embedding text)
	if err = b.storage.Querier().MarkStaleSearchSync(ctx, catalogdb.MarkStaleSearchSyncParams{
		RefType: catalogdb.CatalogSearchSyncRefTypeProductSpu,
		RefID:   params.ID,
	}); err != nil {
		return zero, fmt.Errorf("db update search sync: %w", err)
	}

	// LAST STEP: Update resources
	resources, err := b.common.UpdateResources(ctx, commonbiz.UpdateResourcesParams{
		Account:     params.Account,
		RefType:     commondb.CommonResourceRefTypeProductSpu,
		RefID:       spu.ID,
		ResourceIDs: params.ResourceIDs,
	})
	if err != nil {
		return zero, fmt.Errorf("update product spu: %w", err)
	}

	m := b.mapProductSpu(spu, b.getCategory(ctx, spu.CategoryID))
	m.Tags = params.Tags
	m.Resources = resources
	m.Specifications = params.Specifications
	return m, nil
}

// assertSellerCurrency enforces that an SPU's currency matches the seller's
// inferred wallet currency, derived from their profile country. This keeps the
// invariant spu.currency == Infer(seller.country) so checkout does not need a
// second FX conversion when debiting the seller's wallet.
func (b *CatalogHandler) assertSellerCurrency(
	ctx restate.Context,
	seller accountmodel.AuthenticatedAccount,
	currency string,
) error {
	profile, err := b.account.GetProfile(ctx, accountbiz.GetProfileParams{
		Issuer:    seller,
		AccountID: seller.ID,
	})
	if err != nil {
		return fmt.Errorf("load seller profile: %w", err)
	}
	expected, err := sharedcurrency.Infer(profile.Country)
	if err != nil {
		return fmt.Errorf("infer seller currency: %w", err)
	}
	if currency != expected {
		return catalogmodel.ErrProductCurrencyMismatch.Fmt(profile.Country, expected, currency)
	}
	return nil
}

type DeleteProductSpuParams struct {
	Account accountmodel.AuthenticatedAccount
	ID      uuid.UUID `validate:"required"`
}

// DeleteProductSpu deletes a product SPU by ID.
func (b *CatalogHandler) DeleteProductSpu(ctx restate.Context, params DeleteProductSpuParams) error {
	if err := validator.Validate(params); err != nil {
		return fmt.Errorf("validate delete product spu: %w", err)
	}

	if err := b.storage.Querier().DeleteProductSpu(ctx, catalogdb.DeleteProductSpuParams{
		ID: []uuid.UUID{params.ID},
	}); err != nil {
		return fmt.Errorf("db delete product spu: %w", err)
	}

	return nil
}

type updateTagsParams struct {
	SpuID uuid.UUID
	Tags  []string
}

// updateTags replaces all tags for the given SPU. It must be called within an existing transaction.
func (b *CatalogHandler) updateTags(ctx restate.Context, q *catalogdb.Queries, params updateTagsParams) error {
	if err := q.DeleteProductSpuTag(ctx, catalogdb.DeleteProductSpuTagParams{
		SpuID: []uuid.UUID{params.SpuID},
	}); err != nil {
		return fmt.Errorf("db delete existing tags for spu %s: %w", params.SpuID, err)
	}

	if len(params.Tags) == 0 {
		return nil
	}

	dbTags, err := q.ListTag(ctx, catalogdb.ListTagParams{
		ID: params.Tags,
	})
	if err != nil {
		return fmt.Errorf("db list tags: %w", err)
	}

	var nonExistingTags []string
	existingTagSet := make(map[string]struct{}, len(dbTags))
	for _, t := range dbTags {
		existingTagSet[t.ID] = struct{}{}
	}
	for _, tag := range params.Tags {
		if _, exists := existingTagSet[tag]; !exists {
			nonExistingTags = append(nonExistingTags, tag)
		}
	}

	if len(nonExistingTags) > 0 {
		var args []catalogdb.CreateCopyDefaultTagParams
		for _, tag := range nonExistingTags {
			args = append(args, catalogdb.CreateCopyDefaultTagParams{
				ID: tag,
			})
		}
		if _, err := q.CreateCopyDefaultTag(ctx, args); err != nil {
			return fmt.Errorf("db create tags: %w", err)
		}
	}

	var args []catalogdb.CreateCopyDefaultProductSpuTagParams
	for _, tag := range params.Tags {
		args = append(args, catalogdb.CreateCopyDefaultProductSpuTagParams{
			SpuID: params.SpuID,
			Tag:   tag,
		})
	}
	if _, err = q.CreateCopyDefaultProductSpuTag(ctx, args); err != nil {
		return fmt.Errorf("db create product spu tags: %w", err)
	}

	return nil
}

// mapProductSpu maps a DB CatalogProductSpu row to the model type using a pre-fetched category.
// Callers should set Rating, Tags, Resources, and Specifications as needed.
func (b *CatalogHandler) mapProductSpu(
	spu catalogdb.CatalogProductSpu,
	category catalogdb.CatalogCategory,
) catalogmodel.ProductSpu {
	return catalogmodel.ProductSpu{
		ID:            spu.ID,
		AccountID:     spu.AccountID,
		Slug:          spu.Slug,
		Category:      mapCategory(category),
		FeaturedSkuID: spu.FeaturedSkuID,
		Name:          spu.Name,
		Description:   spu.Description,
		IsEnabled:     spu.IsEnabled,
		Currency:      spu.Currency,
		DateCreated:   spu.DateCreated,
		DateUpdated:   spu.DateUpdated,
	}
}

// mapCategory converts a DB CatalogCategory row to the model type.
func mapCategory(c catalogdb.CatalogCategory) catalogmodel.Category {
	return catalogmodel.Category{
		ID:          c.ID,
		Name:        c.Name,
		Description: c.Description,
		ParentID:    c.ParentID,
	}
}

// GenerateSlug creates a URL-friendly slug from a product name with a unique suffix.
func GenerateSlug(name string) string {
	return fmt.Sprintf("%s.%s", slug.Make(name), uuid.NewString())
}
