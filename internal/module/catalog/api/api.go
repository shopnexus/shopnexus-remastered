// Package catalogapi is the published contract of the catalog service.
//
// Other modules and the gateway depend on this, never on the service package. Methods
// are added one slice at a time, matching api/openapi/*.yaml.
package catalogapi

import "context"

type Service interface {
	// --- categories: the browse tree. Reading is public, writing is admin-only.
	ListCategories(ctx context.Context, req ListCategoriesRequest) ([]Category, error)
	AdminCreateCategory(ctx context.Context, req CreateCategoryRequest) (Category, error)
	AdminUpdateCategory(ctx context.Context, req UpdateCategoryRequest) (Category, error)
	AdminDeleteCategory(ctx context.Context, req DeleteCategoryRequest) error

	// --- tags: reading is public, writing is admin-only ---
	ListTags(ctx context.Context, req ListTagsRequest) (TagPage, error)
	AdminPutTag(ctx context.Context, req PutTagRequest) (Tag, error)
	AdminDeleteTag(ctx context.Context, req DeleteTagRequest) error

	// --- listings: the seller's write model. Every write answers with the whole listing,
	// because a variant has no read of its own.
	CreateListing(ctx context.Context, req CreateListingRequest) (ListingDetail, error)
	GetListing(ctx context.Context, req GetListingRequest) (ListingDetail, error)

	// --- stock: called by order, not by a route ---
	// ReserveStock holds units for a checkout that has not completed. Answers
	// 409 insufficient_stock when there is no room, which the caller acts on.
	ReserveStock(ctx context.Context, req StockMovementRequest) error
	ReleaseStock(ctx context.Context, req StockMovementRequest) error
	// CommitStock turns a reservation into a sale.
	CommitStock(ctx context.Context, req StockMovementRequest) error
}
