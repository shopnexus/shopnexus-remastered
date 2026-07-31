// Package catalogtest provides a stub catalogapi.Service for tests.
//
// A test that cares about one method should not have to write the rest. Embed Stub and
// override what the test is about; anything left over answers 501, so an unstubbed call shows
// up as an obviously wrong status rather than as a plausible zero value.
package catalogtest

import (
	"context"

	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/shared/errx"
)

// Stub implements catalogapi.Service by refusing everything.
type Stub struct{}

var _ catalogapi.Service = Stub{}

func (Stub) ListCategories(context.Context, catalogapi.ListCategoriesRequest) ([]catalogapi.Category, error) {
	return nil, errx.ErrNotImplemented
}

func (Stub) AdminCreateCategory(context.Context, catalogapi.CreateCategoryRequest) (catalogapi.Category, error) {
	return catalogapi.Category{}, errx.ErrNotImplemented
}

func (Stub) AdminUpdateCategory(context.Context, catalogapi.UpdateCategoryRequest) (catalogapi.Category, error) {
	return catalogapi.Category{}, errx.ErrNotImplemented
}

func (Stub) AdminDeleteCategory(context.Context, catalogapi.DeleteCategoryRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) ListTags(context.Context, catalogapi.ListTagsRequest) (catalogapi.TagPage, error) {
	return catalogapi.TagPage{}, errx.ErrNotImplemented
}

func (Stub) AdminPutTag(context.Context, catalogapi.PutTagRequest) (catalogapi.Tag, error) {
	return catalogapi.Tag{}, errx.ErrNotImplemented
}

func (Stub) AdminDeleteTag(context.Context, catalogapi.DeleteTagRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) ReserveStock(context.Context, catalogapi.StockMovementRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) ReleaseStock(context.Context, catalogapi.StockMovementRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) CommitStock(context.Context, catalogapi.StockMovementRequest) error {
	return errx.ErrNotImplemented
}
