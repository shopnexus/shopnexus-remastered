package catalogbiz

import (
	"fmt"

	restate "github.com/restatedev/sdk-go"

	catalogdb "shopnexus-server/internal/module/catalog/db/sqlc"
	catalogmodel "shopnexus-server/internal/module/catalog/model"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/samber/lo"
)

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

	categories, err := b.HydrateCategories(ctx, lo.Map(dbCategories,
		func(row catalogdb.SearchCategoryRow, _ int) catalogdb.CatalogCategory { return row.CatalogCategory },
	))
	if err != nil {
		return zero, fmt.Errorf("hydrate categories: %w", err)
	}

	return paginate.PaginateResult[catalogmodel.Category]{
		PageParams: params.Params,
		Data:       categories,
		Total:      total,
	}, nil
}
