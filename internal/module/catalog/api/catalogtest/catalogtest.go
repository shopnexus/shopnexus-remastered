// Package catalogtest provides a stub catalogapi.Service for tests.
//
// A test that cares about one method should not have to write the rest. Embed Stub and
// override what the test is about; anything left over answers 501, so an unstubbed call shows
// up as an obviously wrong status rather than as a plausible zero value.
package catalogtest

import (
	"context"

	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/module/common"
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

func (Stub) ListListings(context.Context, catalogapi.ListListingsRequest) (catalogapi.ListingPage, error) {
	return catalogapi.ListingPage{}, errx.ErrNotImplemented
}

func (Stub) AddFavorite(context.Context, catalogapi.FavoriteRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) RemoveFavorite(context.Context, catalogapi.FavoriteRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) CreateListing(context.Context, catalogapi.CreateListingRequest) (catalogapi.ListingDetail, error) {
	return catalogapi.ListingDetail{}, errx.ErrNotImplemented
}

func (Stub) GetListing(context.Context, catalogapi.GetListingRequest) (catalogapi.ListingDetail, error) {
	return catalogapi.ListingDetail{}, errx.ErrNotImplemented
}

func (Stub) CreateVariant(context.Context, catalogapi.CreateVariantRequest) (catalogapi.ListingDetail, error) {
	return catalogapi.ListingDetail{}, errx.ErrNotImplemented
}

func (Stub) UpdateVariant(context.Context, catalogapi.UpdateVariantRequest) (catalogapi.ListingDetail, error) {
	return catalogapi.ListingDetail{}, errx.ErrNotImplemented
}

func (Stub) DeleteVariant(context.Context, catalogapi.DeleteVariantRequest) (catalogapi.ListingDetail, error) {
	return catalogapi.ListingDetail{}, errx.ErrNotImplemented
}

func (Stub) UpdateListing(context.Context, catalogapi.UpdateListingRequest) (catalogapi.ListingDetail, error) {
	return catalogapi.ListingDetail{}, errx.ErrNotImplemented
}

func (Stub) DeleteListing(context.Context, catalogapi.DeleteListingRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) PublishListing(context.Context, catalogapi.PublishListingRequest) (catalogapi.ListingDetail, error) {
	return catalogapi.ListingDetail{}, errx.ErrNotImplemented
}

func (Stub) HideListing(context.Context, catalogapi.HideListingRequest) (catalogapi.ListingDetail, error) {
	return catalogapi.ListingDetail{}, errx.ErrNotImplemented
}

func (Stub) AdminListListings(context.Context, catalogapi.AdminListListingsRequest) (catalogapi.ListingPage, error) {
	return catalogapi.ListingPage{}, errx.ErrNotImplemented
}

func (Stub) AdminApproveListing(context.Context, catalogapi.ApproveListingRequest) (catalogapi.ListingDetail, error) {
	return catalogapi.ListingDetail{}, errx.ErrNotImplemented
}

func (Stub) AdminTakedownListing(context.Context, catalogapi.TakedownListingRequest) (catalogapi.ListingDetail, error) {
	return catalogapi.ListingDetail{}, errx.ErrNotImplemented
}

func (Stub) ReserveStock(context.Context, catalogapi.StockMovementRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) ReleaseStock(context.Context, catalogapi.StockMovementRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) CommitStock(context.Context, catalogapi.StockCommitRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) UncommitStock(context.Context, catalogapi.StockCommitRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) SyncListingRating(context.Context, catalogapi.SyncListingRatingRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) CreateUpload(context.Context, catalogapi.CreateUploadRequest) (catalogapi.UploadSlot, error) {
	return catalogapi.UploadSlot{}, errx.ErrNotImplemented
}

func (Stub) ConfirmUpload(context.Context, catalogapi.ConfirmUploadRequest) (common.ResourceDTO, error) {
	return common.ResourceDTO{}, errx.ErrNotImplemented
}
