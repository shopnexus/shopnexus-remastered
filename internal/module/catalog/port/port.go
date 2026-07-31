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
}
