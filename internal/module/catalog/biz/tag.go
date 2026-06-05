package catalogbiz

import (
	restate "github.com/restatedev/sdk-go"

	accountmodel "shopnexus-server/internal/module/account/model"
	catalogdb "shopnexus-server/internal/module/catalog/db/sqlc"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/validator"

	"github.com/guregu/null/v6"
	"github.com/samber/lo"
)

type ListTagParams struct {
	paginate.Params

	Search null.String `validate:"omitnil,max=100"`
}

// ListTag returns paginated tags with optional text search.
func (b *CatalogHandler) ListTag(
	ctx restate.Context,
	params ListTagParams,
) (paginate.PaginateResult[catalogdb.CatalogTag], error) {
	var zero paginate.PaginateResult[catalogdb.CatalogTag]

	if err := validator.Validate(params); err != nil {
		return zero, err
	}

	listTag, err := b.storage.Querier().SearchTag(ctx, catalogdb.SearchTagParams{
		Search: params.Search,
		Limit:  params.Limit,
		Offset: params.Offset(),
	})
	if err != nil {
		return zero, err
	}

	var total null.Int64
	if len(listTag) > 0 {
		total.SetValid(listTag[0].TotalCount)
	}

	return paginate.PaginateResult[catalogdb.CatalogTag]{
		PageParams: params.Params,
		Total:      total,
		Data: lo.Map(
			listTag,
			func(row catalogdb.SearchTagRow, _ int) catalogdb.CatalogTag { return row.CatalogTag },
		),
	}, nil
}

type GetTagParams struct {
	Account accountmodel.AuthenticatedAccount
	Tag     string `validate:"required,min=1,max=100"`
}

// GetTag returns a single tag by its name.
func (b *CatalogHandler) GetTag(ctx restate.Context, params GetTagParams) (catalogdb.CatalogTag, error) {
	var zero catalogdb.CatalogTag

	if err := validator.Validate(params); err != nil {
		return zero, err
	}

	tag, err := b.storage.Querier().GetTag(ctx, null.StringFrom(params.Tag))
	if err != nil {
		return zero, err
	}

	return tag, nil
}
