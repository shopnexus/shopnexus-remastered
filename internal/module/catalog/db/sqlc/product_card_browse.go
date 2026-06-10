package catalogdb

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"

	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/repolist"
)

// ListProductCardBrowseParams is the no-vector browse filter for product cards.
type ListProductCardBrowseParams struct {
	paginate.Params

	ID         []uuid.UUID // pre-resolved id set (e.g. tag pre-filter)
	AccountID  []uuid.UUID
	CategoryID []uuid.UUID
	Search     null.String // ILIKE across name/description/slug (vector-search fallback)
}

// productCardSortAlias maps the API sort vocabulary to the denormalized columns
// keyset paging actually runs on. The search path keeps the same vocabulary (see
// hybridSortExprs), so price/rating sort behaves identically across both paths.
var productCardSortAlias = map[string]string{
	"price":  "cached_price",
	"rating": "cached_rating",
}

// remapSort rewrites a `?sort` string through productCardSortAlias, preserving
// per-field direction. Unaliased fields (id, date_created) pass through.
func remapSort(raw string) string {
	fields := paginate.ParseSort(raw)
	if len(fields) == 0 {
		return raw
	}
	parts := make([]string, len(fields))
	for i, s := range fields {
		col := s.Field
		if mapped, ok := productCardSortAlias[col]; ok {
			col = mapped
		}
		if s.Dir == paginate.Desc {
			col = "-" + col
		}
		parts[i] = col
	}
	return strings.Join(parts, ",")
}

// ListProductCardBrowse lists live product-spu rows by scalar filters, paginated +
// sorted by the shared list runtime (offset ?page or keyset ?cursor/?sort). It
// composes the generated ProductSpuQuery base — reusing its Fields/Sort/scan —
// with a soft-delete guard and the OR'd ILIKE fallback. The API price/rating sort
// keys are remapped to the cached_* columns before paging (see remapSort).
func (q *Queries) ListProductCardBrowse(
	ctx context.Context,
	arg ListProductCardBrowseParams,
) (paginate.PaginateResult[CatalogProductSpu], error) {
	conds := []repolist.Cond{
		repolist.Raw(`"date_deleted" IS NULL`),
		repolist.In(`"id"`, arg.ID),
		repolist.In(`"account_id"`, arg.AccountID),
		repolist.In(`"category_id"`, arg.CategoryID),
	}
	if arg.Search.Valid {
		conds = append(conds, repolist.Expr(
			`("name" ILIKE @q OR "description" ILIKE @q OR "slug" ILIKE @q)`,
			"q", "%"+arg.Search.String+"%"))
	}

	params := arg.Params
	params.Sort = remapSort(params.Sort)
	return repolist.List(ctx, q.db, params, ProductSpuQuery().Filter(conds...))
}
