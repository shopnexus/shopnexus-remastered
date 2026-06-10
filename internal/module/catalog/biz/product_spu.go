package catalogbiz

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/gosimple/slug"
	restate "github.com/restatedev/sdk-go"
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

type GetProductSpuParams struct {
	ID   uuid.NullUUID `validate:"omitnil"`
	Slug null.String   `validate:"omitnil"`
}

// GetProductSpu returns a single product SPU by ID or slug.
func (b *CatalogHandler) GetProductSpu(
	ctx context.Context,
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
	ctx context.Context,
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

	spus, err := b.HydrateProductSpus(ctx, dbSpus)
	if err != nil {
		return zero, fmt.Errorf("hydrate product spus: %w", err)
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

	// decision: enforce the seller-currency invariant before writing.
	if err := b.assertSellerCurrency(ctx, params.Account, params.Currency); err != nil {
		return zero, fmt.Errorf("assert seller currency: %w", err)
	}

	specsBytes, err := json.Marshal(params.Specifications)
	if err != nil {
		return zero, fmt.Errorf("create product spu: %w", err)
	}

	// execution: create the SPU with its tags and search-sync entry.
	spu, err := restate.Run(ctx, func(rctx restate.RunContext) (catalogdb.CatalogProductSpu, error) {
		spu, err := b.storage.Querier().CreateDefaultProductSpu(rctx, catalogdb.CreateDefaultProductSpuParams{
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
			return catalogdb.CatalogProductSpu{}, fmt.Errorf("db create product spu: %w", err)
		}

		if err := b.updateTags(rctx, b.storage.Querier(), updateTagsParams{
			SpuID: spu.ID,
			Tags:  params.Tags,
		}); err != nil {
			return catalogdb.CatalogProductSpu{}, fmt.Errorf("create product spu: %w", err)
		}

		if _, err := b.storage.Querier().CreateDefaultSearchSync(rctx, catalogdb.CreateDefaultSearchSyncParams{
			RefType: catalogdb.CatalogSearchSyncRefTypeProductSpu,
			RefID:   spu.ID,
		}); err != nil {
			return catalogdb.CatalogProductSpu{}, fmt.Errorf("db create search sync: %w", err)
		}
		return spu, nil
	})
	if err != nil {
		return zero, err
	}

	// Create resources
	if _, err := b.common.Call().UpdateResources(ctx, commonbiz.UpdateResourcesParams{
		Account:     params.Account,
		RefType:     commondb.CommonResourceRefTypeProductSpu,
		RefID:       spu.ID,
		ResourceIDs: params.ResourceIDs,
	}); err != nil {
		return zero, fmt.Errorf("create product spu: %w", err)
	}

	spus, err := b.HydrateProductSpus(ctx, []catalogdb.CatalogProductSpu{spu})
	if err != nil {
		return zero, fmt.Errorf("hydrate created product spu: %w", err)
	}
	return spus[0], nil
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

	var slug null.String
	if params.RegenerateSlug && params.Name.Valid {
		slug.SetValid(GenerateSlug(params.Name.String))
	}

	specsBytes, err := json.Marshal(params.Specifications)
	if err != nil {
		return zero, fmt.Errorf("marshal product spu specifications: %w", err)
	}

	// decision: validate currency + featured SKU, then write the SPU, tags, and
	// stale-search marker in one journaled step.
	spu, err := restate.Run(ctx, func(rctx restate.RunContext) (catalogdb.CatalogProductSpu, error) {
		if params.Currency.Valid {
			if err := b.assertSellerCurrency(rctx, params.Account, params.Currency.String); err != nil {
				return catalogdb.CatalogProductSpu{}, fmt.Errorf("assert seller currency: %w", err)
			}
		}

		// Ensure the featured SKU (if provided) belongs to the current SPU.
		if params.FeaturedSkuID.Valid {
			skus, err := b.storage.Querier().ListProductSku(rctx, catalogdb.ListProductSkuParams{
				Id: []uuid.UUID{params.FeaturedSkuID.UUID},
			})
			if err != nil {
				return catalogdb.CatalogProductSpu{}, fmt.Errorf("db validate featured sku: %w", err)
			}
			if len(skus.Data) == 0 || skus.Data[0].SpuID != params.ID {
				return catalogdb.CatalogProductSpu{}, catalogmodel.ErrSkuNotBelongToSpu
			}
		}

		spu, err := b.storage.Querier().UpdateProductSpu(rctx, catalogdb.UpdateProductSpuParams{
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
			return catalogdb.CatalogProductSpu{}, fmt.Errorf("db update product spu: %w", err)
		}

		if err := b.updateTags(rctx, b.storage.Querier(), updateTagsParams{
			SpuID: spu.ID,
			Tags:  params.Tags,
		}); err != nil {
			return catalogdb.CatalogProductSpu{}, fmt.Errorf("update product spu tags: %w", err)
		}

		// Mark the search embedding stale (name/description/tags feed the embedding text).
		if err := b.storage.Querier().MarkStaleSearchSync(rctx, catalogdb.MarkStaleSearchSyncParams{
			RefType: catalogdb.CatalogSearchSyncRefTypeProductSpu,
			RefID:   params.ID,
		}); err != nil {
			return catalogdb.CatalogProductSpu{}, fmt.Errorf("db update search sync: %w", err)
		}
		return spu, nil
	})
	if err != nil {
		return zero, err
	}

	// execution: update resources (cross-module).
	if _, err := b.common.Call().UpdateResources(ctx, commonbiz.UpdateResourcesParams{
		Account:     params.Account,
		RefType:     commondb.CommonResourceRefTypeProductSpu,
		RefID:       spu.ID,
		ResourceIDs: params.ResourceIDs,
	}); err != nil {
		return zero, fmt.Errorf("update product spu resources: %w", err)
	}

	spus, err := b.HydrateProductSpus(ctx, []catalogdb.CatalogProductSpu{spu})
	if err != nil {
		return zero, fmt.Errorf("hydrate updated product spu: %w", err)
	}
	return spus[0], nil
}

// assertSellerCurrency enforces that an SPU's currency matches the seller's
// inferred wallet currency, derived from their profile country. This keeps the
// invariant spu.currency == Infer(seller.country) so checkout does not need a
// second FX conversion when debiting the seller's wallet.
func (b *CatalogHandler) assertSellerCurrency(
	ctx context.Context,
	seller accountmodel.AuthenticatedAccount,
	currency string,
) error {
	profile, err := b.account.GetProfile(ctx, accountbiz.GetProfileParams{
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

	// execution: delete the SPU.
	return restate.RunVoid(ctx, func(rctx restate.RunContext) error {
		if err := b.storage.Querier().DeleteProductSpu(rctx, catalogdb.DeleteProductSpuParams{
			ID: []uuid.UUID{params.ID},
		}); err != nil {
			return fmt.Errorf("db delete product spu: %w", err)
		}
		return nil
	})
}

type updateTagsParams struct {
	SpuID uuid.UUID
	Tags  []string
}

// updateTags replaces all tags for the given SPU. It must be called within an existing transaction.
func (b *CatalogHandler) updateTags(ctx context.Context, q *catalogdb.Queries, params updateTagsParams) error {
	if err := q.DeleteProductSpuTag(ctx, catalogdb.DeleteProductSpuTagParams{
		SpuID: []uuid.UUID{params.SpuID},
	}); err != nil {
		return fmt.Errorf("db delete existing tags for spu %s: %w", params.SpuID, err)
	}

	if len(params.Tags) == 0 {
		return nil
	}

	dbTags, err := q.ListTag(ctx, catalogdb.ListTagParams{
		Id: params.Tags,
	})
	if err != nil {
		return fmt.Errorf("db list tags: %w", err)
	}

	var nonExistingTags []string
	existingTagSet := make(map[string]struct{}, len(dbTags.Data))
	for _, t := range dbTags.Data {
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

// GenerateSlug creates a URL-friendly slug from a product name with a unique suffix.
func GenerateSlug(name string) string {
	return fmt.Sprintf("%s.%s", slug.Make(name), uuid.NewString())
}
