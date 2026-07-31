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

// Seed is one `near` seed: exactly one field is set. A tag slug and a category id are both
// legal because every embedding column in this schema is vector(1024) in one BGE-M3 space, so
// the distance between a category and a tag means the same as between two tags.
type Seed struct {
	TagSlug    string
	CategoryID int64
}

// Vector is a dense embedding as pgvector stores it.
type Vector []float32

type ScoredCategory struct {
	Category domain.Category
	Score    float64
}

type ScoredTag struct {
	Tag   domain.Tag
	Score float64
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

	// --- semantic suggestion ---
	// SeedVectors reads one vector per seed, in the order asked, nil where the embedding pass
	// has not written one yet — so the service can name the seed it rejects.
	SeedVectors(ctx context.Context, seeds []Seed) ([]Vector, error)
	NearestCategories(ctx context.Context, vectors []Vector, limit int) ([]ScoredCategory, error)
	NearestTags(ctx context.Context, vectors []Vector, exclude []string, offset, limit int) ([]ScoredTag, error)
}
