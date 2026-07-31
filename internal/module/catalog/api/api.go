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
}
