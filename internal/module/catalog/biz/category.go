package catalogbiz

import (
	"fmt"

	restate "github.com/restatedev/sdk-go"

	catalogdb "shopnexus-server/internal/module/catalog/db/sqlc"
	catalogmodel "shopnexus-server/internal/module/catalog/model"
	commonbiz "shopnexus-server/internal/module/common/biz"
	commondb "shopnexus-server/internal/module/common/db/sqlc"
	commonmodel "shopnexus-server/internal/module/common/model"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/samber/lo"
)

const popularProductLimit = 4

type ListCategoryParams struct {
	paginate.Params

	ID     []uuid.UUID `validate:"omitempty,dive,gt=0"`
	Search null.String `validate:"omitnil"`
}

// ListCategory returns paginated categories with popular product images.
func (b *CatalogHandler) ListCategory(
	ctx restate.Context,
	params ListCategoryParams,
) (paginate.PaginateResult[catalogmodel.Category], error) {
	var zero paginate.PaginateResult[catalogmodel.Category]

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate list category params: %w", err)
	}

	dbCategories, err := b.storage.Querier().SearchCategory(ctx, catalogdb.SearchCategoryParams{
		ID:     params.ID,
		Search: params.Search,
		Limit:  params.Limit,
		Offset: params.Offset(),
	})
	if err != nil {
		return zero, fmt.Errorf("db search category: %w", err)
	}

	var total null.Int64
	if len(dbCategories) > 0 {
		total.SetValid(dbCategories[0].TotalCount)
	}

	categoryIDs := lo.Map(dbCategories, func(row catalogdb.SearchCategoryRow, _ int) uuid.UUID {
		return row.CatalogCategory.ID
	})

	// Get popular product SPU IDs per category
	popularProducts, err := b.storage.Querier().
		ListPopularProductPerCategory(ctx, catalogdb.ListPopularProductPerCategoryParams{
			CategoryID:   categoryIDs,
			ProductLimit: popularProductLimit,
		})
	if err != nil {
		return zero, fmt.Errorf("list popular products per category: %w", err)
	}

	// Fetch resources (images) for all popular product SPUs
	spuIDs := lo.Map(popularProducts, func(row catalogdb.ListPopularProductPerCategoryRow, _ int) uuid.UUID {
		return row.SpuID
	})

	resourcesMap, err := b.common.GetResources(ctx, commonbiz.GetResourcesParams{
		RefType: commondb.CommonResourceRefTypeProductSpu,
		RefIDs:  spuIDs,
	})
	if err != nil {
		return zero, fmt.Errorf("get popular product resources: %w", err)
	}

	// Group: categoryID -> first resource of each popular product
	categoryResourcesMap := make(map[uuid.UUID][]commonmodel.Resource)
	for _, row := range popularProducts {
		resources := resourcesMap[row.SpuID]
		if len(resources) > 0 {
			categoryResourcesMap[row.CategoryID] = append(categoryResourcesMap[row.CategoryID], resources[0])
		}
	}

	return paginate.PaginateResult[catalogmodel.Category]{
		PageParams: params.Params,
		Data: lo.Map(dbCategories, func(row catalogdb.SearchCategoryRow, _ int) catalogmodel.Category {
			return catalogmodel.Category{
				ID:          row.CatalogCategory.ID,
				Name:        row.CatalogCategory.Name,
				Description: row.CatalogCategory.Description,
				ParentID:    row.CatalogCategory.ParentID,
				Resources:   categoryResourcesMap[row.CatalogCategory.ID],
			}
		}),
		Total: total,
	}, nil
}
