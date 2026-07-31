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

// AuditEntry is one row of the module's audit log. SaveListing writes these itself from the
// aggregate's events.
type AuditEntry struct {
	Table      string
	RecordID   int64
	ChangeType string
	// Code is the business event, e.g. "listing.publish".
	Code string
	// ChangedBy is nil for a change no account is responsible for (a scheduled job).
	ChangedBy *int64
	// Diff and Snapshot are whatever the recorder declared — a domain event's payload and a
	// row snapshot — and reach the JSONB columns through json.Marshal. `any` because the
	// shape belongs to the fact, not to the trail.
	Diff     any
	Snapshot any
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

	// --- the listing aggregate: load it, change it in memory, save it ---

	// CreateListing writes the listing, its variants, their stock rows and its tag joins in
	// one transaction. The create request carries the variants inline, so there is no window
	// in which a listing has nothing to sell.
	CreateListing(ctx context.Context, l *domain.Listing, actor int64) error
	// GetListing reads the root with its children, and is the only loader — which is what
	// makes exported children and a state-based Save safe.
	GetListing(ctx context.Context, id int64) (*domain.Listing, error)
	// GetListingForSeller scopes the read by owner, so another seller's listing is not found
	// rather than forbidden.
	GetListingForSeller(ctx context.Context, id, sellerID int64) (*domain.Listing, error)
	// SaveListing validates the aggregate and writes the root, its variants, its tags and
	// the audit rows for what it recorded in one transaction, guarded by Version. A stale
	// copy gets domain.ErrVersionConflict.
	//
	// Variants are synchronised to the slice, tags by negation. A variant absent from the
	// slice is *soft* deleted: order.item holds variant_id without a foreign key.
	SaveListing(ctx context.Context, l *domain.Listing, actor int64) error
	SlugTaken(ctx context.Context, slug string) (bool, error)

	// --- wishlist reads. The routes that write it are another slice; the product page needs
	// these two now, and answering them from another module would be a call per card.
	IsFavorited(ctx context.Context, accountID, listingID int64) (bool, error)
	CountFavorites(ctx context.Context, listingID int64) (int64, error)

	// --- stock: its own aggregate, one row per variant ---
	// Each of these is a single guarded statement: the WHERE clause is the invariant, so no
	// version is involved and two checkouts on different variants never contend. Zero rows
	// affected is the refusal, not an error to wrap.
	//
	// ReserveStock holds units for a checkout that has not completed.
	ReserveStock(ctx context.Context, variantID, units int64) error
	// ReleaseStock gives them back — a cancelled or expired session.
	ReleaseStock(ctx context.Context, variantID, units int64) error
	// CommitStock turns a reservation into a sale and bumps listing.cached_sold in the same
	// transaction, so the badge and the counter cannot drift apart.
	CommitStock(ctx context.Context, variantID, units int64) error
	FindStock(ctx context.Context, variantID int64) (domain.Stock, error)
}
