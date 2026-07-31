// Package port: the interface the catalog adapter must satisfy.
//
// It speaks in raw int64 keys and domain entities — opaque ids stop at the api boundary.
// Methods are added one slice at a time; the dictionaries come first because a listing
// cannot reference a category that has no way of existing.
package port

import (
	"context"

	"shopnexus/internal/module/catalog/domain"
)

// TagFilter drives the tag picker: Prefix is matched against the head of the slug, which
// is what a picker types into, and the page is what keeps a growing dictionary answerable.
type TagFilter struct {
	Prefix string
	Offset int
	Limit  int
}

type Repository interface {
	// --- category: the browse tree. Small and curated, so the whole of it is one read
	// and a client assembles the shape.
	ListCategories(ctx context.Context) ([]domain.Category, error)
	CategoryExists(ctx context.Context, id int64) (bool, error)
	CreateCategory(ctx context.Context, c *domain.Category) error
	// UpdateCategory writes the row and, when ParentID changed, moves it — one guarded
	// statement, because a move is legal only if the new parent is not a descendant and
	// only the database can see the path. Answers ErrCategoryCycle when it is.
	UpdateCategory(ctx context.Context, c domain.Category) error
	DeleteCategory(ctx context.Context, id int64) error

	// --- tags: a flat dictionary keyed by its slug ---
	ListTags(ctx context.Context, f TagFilter) ([]domain.Tag, int64, error)
	// PutTag is an upsert, so the admin route is idempotent: the slug is in the path and
	// only the description can change.
	PutTag(ctx context.Context, t domain.Tag) error
	DeleteTag(ctx context.Context, slug string) error
}
