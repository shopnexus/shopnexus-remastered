// Package port: the interface the catalog adapter must satisfy.
//
// It speaks in raw int64 keys and domain entities — opaque ids stop at the api boundary.
// Methods are added one slice at a time; the dictionaries come first because a listing
// cannot reference a category that has no way of existing.
package port

import (
	"context"
	"time"

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

// Probe is one text's embedding as retrieval uses it: both halves out of a single pass over the
// text. Separate from Vector, which stays dense-only because an interest is `avg(dense)`
// computed in SQL and there is no meaningful average of sparse vectors.
type Probe struct {
	Dense  Vector
	Sparse map[uint32]float32
}

type ScoredCategory struct {
	Category domain.Category
	Score    float64
}

type ScoredTag struct {
	Tag   domain.Tag
	Score float64
}

// ListingSummary is the moderation queue row: the flat shape a table renders, so a page of
// twenty is one query rather than twenty aggregate loads. Price is the featured variant's,
// resolved in the same statement.
type ListingSummary struct {
	ID        int64
	SellerID  int64
	Slug      string
	Name      string
	Status    domain.Status
	Condition domain.Condition
	PriceMode domain.PriceMode
	Currency  string
	Price     int64
	Sold      int64
	Rating    float64
	// ReviewCount is how many reviews the rating averages: a 5.0 from one review and a 5.0
	// from two hundred are not the same claim.
	ReviewCount int64
	// Score is the search's, and only a search sets it: higher is better whichever mode ran.
	Score      *float64
	CategoryID int64
	CoverID    *int64
	// Tags are the listing's own, read on the card so a client can render chips without a
	// request per row. Empty rather than nil for a listing with none.
	Tags []string
	// TakenDownAt says staff removed this one, which `hidden` alone cannot: a seller who hid
	// their own listing reads the same status.
	TakenDownAt *time.Time
	CreatedAt   time.Time
	// DeletedAt is set on a listing the seller removed. Only an `ids` lookup returns one — an
	// order that references it still has to render.
	DeletedAt *time.Time
	// Location is where the goods are, and nil for a listing that was never published. DistanceKM
	// is set only when the browse named a position.
	Location   *domain.Location
	DistanceKM *float64
}

// AuditRecord is one row of the listing's trail, as the history read needs it. The snapshot
// column is deliberately absent: it is the whole row as it then was, and nothing outside the
// database has a use for it that would survive the row growing a column.
type AuditRecord struct {
	Version    int64
	Code       string
	ChangeType string
	ChangedAt  time.Time
	// ChangedBy is nil for a change no account is responsible for.
	ChangedBy *int64
	// Diff is the payload the recorder declared, decoded as it was stored.
	Diff map[string]any
}

// ListingFilter is the browse feed: one query narrowed by parameters rather than a family of
// endpoints. Zero values mean "not filtered", so a bare filter is "the newest live listings".
type ListingFilter struct {
	// IDs resolves a known set and ignores every other filter — a cart or an order list
	// rendering the listings behind its rows.
	IDs []int64
	// VariantIDs resolves the listings those variants belong to, and ignores the rest of
	// the filter for the same reason IDs does.
	VariantIDs []int64
	// ExcludeIDs drops rows whatever else matched them. Today it holds the listing a
	// "similar to this one" ranking is *about*: its own embedding is its own nearest
	// neighbour, so without this the first card of every such rail is the listing the
	// reader is already looking at.
	ExcludeIDs []int64
	// Query turns the request into a search, and the words themselves rank nothing: Terms carries
	// the probe of them. What is left for the raw string is the recommended feed's name ILIKE,
	// which is the one path a search's terms never reach.
	Query string
	// Terms are the compiled ranking signals: a probe to rank against, or a row predicate that
	// contributes when it holds. Weight is already folded, so the adapter never sees an
	// attribute name, a position or anything a model wrote.
	Terms []Term
	// Interests is what a recommended feed ranks against, strongest first. Several probes
	// rather than one, because a buyer is several buyers: phones on Monday and a bicycle on
	// Thursday are not one taste whose average is a taste for neither. Empty means the
	// account has none computed, and the service falls back to newest.
	Interests []Interest
	// Seed decides which of the many good orderings of a personalised feed this caller gets.
	// The merge draws from a pool several pages deep instead of taking the top of it, and the
	// draw is a function of this and the listing's id — so one seed always produces the same
	// order (page two follows page one) and a new one produces a different feed. Resolved by
	// the service, never empty for a personalised feed.
	Seed string
	// SkipTotal drops the count behind the page. Not an optimisation to reach for casually —
	// it is for callers that never read it. The count is `COUNT(*) OVER ()`, a window function
	// that makes Postgres run the whole feed statement over every matching row before LIMIT
	// applies: measured on 920 344 active listings, the same query is 4.4ms without it and
	// 1 165ms with it. The home page's shelves discarded three of these per request.
	SkipTotal bool
	// MatchedOnly drops the personalised feed's fresh source, leaving only what the interest
	// legs actually matched. For a named row, whose heading is a claim about its cards.
	MatchedOnly bool
	// ViewerID is the caller, needed for Mine, Favorited and Recommended. Zero is anonymous.
	ViewerID   int64
	Mine       bool
	Favorited  bool
	Status     domain.Status
	CategoryID int64
	Tag        string
	SellerID   int64
	Condition  domain.Condition
	MinPrice   int64
	// MaxPrice is a pointer because 0 is a legal, meaningful bound ("match nothing":
	// every price is gte=1) rather than "not filtered" — unlike MinPrice, where 0 really
	// is a no-op since it excludes no price a listing could have.
	MaxPrice *int64
	// The administrative filter, narrowest level wins: a ward implies its district and province,
	// so a caller sends the one they mean. Matched against the listing's own snapshot.
	ProvinceCode string
	DistrictCode string
	WardCode     string
	// Near is where the buyer is. With it, every row carries its distance and a radius may bound
	// the result; without it, `distance` is not a sort that can be answered.
	Near     *Point
	RadiusKM float64
	Sort     string
	Offset   int
	Limit    int
}

// Interest is one of the things an account keeps coming back to: a direction in embedding
// space, and how much of their behaviour points that way. Weight is a share of the whole
// signal, so the weights of an account's interests sum to 1 and a feed can hand each of them
// a proportional slice of the page.
type Interest struct {
	Vector Vector
	Weight float64
}

// ListingSignal is one row of listing_signal: a shopper's action against a listing, the source
// interestSignals reads next to favorite. AccountID is never 0 here — an anonymous
// ListingInteraction is a popularity signal only, and observability's own subscriber is what
// reads that one; this table exists for personalisation, which has nothing to attach an
// anonymous action to.
type ListingSignal struct {
	AccountID int64
	ListingID int64
	Type      string
}

// Point is a WGS84 coordinate — the buyer's position for a "near me" browse.
type Point struct {
	Latitude  float64 `validate:"gte=-90,lte=90"`
	Longitude float64 `validate:"gte=-180,lte=180"`
}

// Term is one contribution to a search's ranking. Exactly one of Probe and Predicate is set.
type Term struct {
	// Weight is w_attr · pos_i · sign, folded by the service. Negative for a demotion.
	Weight float64
	Probe  *Probe
	// Predicate contributes at rank 1 for every row that satisfies it, which is what puts it
	// in the same units as a probe's rank without a second scale to calibrate.
	Predicate *Predicate
}

// Predicate is a row test from a fixed set. Kind fixes the type of Value, which is bound as a
// query parameter — nothing here reaches the SQL text.
type Predicate struct {
	Kind  string
	Value any
}

// No province or ward here: the understanding stage has no vocabulary of places to copy from —
// the knowledge base shows categories, tags and titles — so a model could only guess at the
// codes these columns hold. The shopper's own location filters are hard predicates in the
// browse's WHERE and are unaffected.
const (
	PredicateCategory  = "category"  // Value int64
	PredicateTag       = "tag"       // Value string (slug)
	PredicateMinPrice  = "min-price" // Value int64
	PredicateMaxPrice  = "max-price" // Value int64
	PredicateCondition = "condition" // Value string
)

// PredicateKinds is the set, listed once so the adapter's whitelist can be checked complete: a
// kind declared with no SQL behind it compiles, reaches the statement and silently ranks nothing.
var PredicateKinds = []string{
	PredicateCategory, PredicateTag, PredicateMinPrice, PredicateMaxPrice, PredicateCondition,
}

// The sorts, spelled once so the service and the adapter agree.
const (
	SortNewest      = "newest"
	SortRating      = "rating"
	SortPriceAsc    = "price-asc"
	SortPriceDesc   = "price-desc"
	SortBestSelling = "best-selling"
	SortRelevance   = "relevance"
	SortRecommended = "recommended"
	SortDistance    = "distance"
	// SortTrending is the platform's own top list — listing_popularity, read back through
	// observabilityapi.Service, ordered by nobody's taste but everybody's aggregate behaviour.
	// Also what a personalised feed falls back to for an account with nothing computed yet,
	// which is what keeps a zero-interest browse from reading as "the newest, forever" — see
	// catalog.Service.feedFilter.
	SortTrending = "trending"
)

// QueueFilter drives the queue. Status empty means both halves of it: a listing waiting for
// its first publication, and a live one holding an edit.
type QueueFilter struct {
	Status   domain.Status
	SellerID int64
	Offset   int
	Limit    int
}

type Repository interface {
	// --- category: the browse tree. Small and curated, so the whole of it is one read
	// and a client assembles the shape.
	ListCategories(ctx context.Context) ([]domain.Category, error)
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
	// SoftDeleteListing marks the row deleted and writes the trail. Soft, because order.item
	// holds listing_id without a foreign key and a past order has to stay renderable.
	SoftDeleteListing(ctx context.Context, id, sellerID, actor int64) error
	// ListListingHistory reads the listing's trail, newest first — the order a history is
	// read in, against the version's own order.
	ListListingHistory(ctx context.Context, listingID int64, offset, limit int) ([]AuditRecord, int64, error)
	// ListModerationQueue answers the moderator's worklist, oldest first — the order it should
	// be worked.
	ListModerationQueue(ctx context.Context, f QueueFilter) ([]ListingSummary, int64, error)

	// --- the browse feed ---

	// ListListings answers the feed: cards from a flat read model, because a page of twenty
	// must not be twenty aggregate loads. Score is set only when the filter was a search.
	ListListings(ctx context.Context, f ListingFilter) ([]ListingSummary, int64, error)
	// ListListingsByIDs hydrates a specific set of ids into cards, active ones only — the
	// personalised feed's cache reads this to refresh a page it drew earlier rather than
	// trusting a stale price. Order is the caller's to keep; an id no longer active simply
	// does not come back.
	ListListingsByIDs(ctx context.Context, ids []int64) ([]ListingSummary, error)
	// Interests reads an account's interest slots, strongest first — what `sort=recommended`
	// ranks against. Empty for an account nothing has computed yet, and the service falls
	// back to newest.
	Interests(ctx context.Context, accountID int64) ([]Interest, error)
	// ListingProbe is a listing's own stored embedding, read back to be used as a query — what
	// "more like this one" ranks against. domain.ErrListingNotEmbedded when the embedding pass
	// has not reached the row yet, which is a listing to fall back for rather than a failure.
	ListingProbe(ctx context.Context, listingID int64) (Probe, error)
	// RecentSignals is what an account did last, most recent first — the rows behind a shelf
	// that says *why* it is there ("because you looked at this"). Types narrows to the kinds
	// worth reasoning from; empty takes them all.
	RecentSignals(ctx context.Context, accountID int64, types []string, limit int) ([]ListingSignal, error)
	// RecomputeInterests rebuilds those slots from what the account saved plus its recent
	// positive listing_signal rows (a view, a click — never a negative one: an average that
	// becomes a share of the page has no business holding a negative number, so
	// "not-interested"/"hidden" instead exclude a listing outright), replacing the set in one
	// transaction so a reader never sees half an account's taste. signalWeights is
	// catalogapi.InteractionWeight narrowed to catalogapi.PositiveInteractionTypes — the
	// service's job, since this layer may not import that package.
	RecomputeInterests(ctx context.Context, accountID int64, signalWeights map[string]float64) error
	// InsertListingSignals writes a batch of shopper actions — this module's own subscriber to
	// its own ListingInteractionTopic, off the request path entirely.
	InsertListingSignals(ctx context.Context, signals []ListingSignal) error
	// StaleInterests names the accounts whose slots no longer reflect their wishlist —
	// something saved or unsaved since, or a saved listing embedded since. The recompute runs
	// inline on a wishlist write; this is the net under the pass that failed, and under the
	// listing whose vector only arrived afterwards.
	StaleInterests(ctx context.Context, limit int) ([]int64, error)

	// --- wishlist writes ---

	// FavoritedAmong answers which of these listings the account saved — one query for a whole
	// page rather than one per card.
	FavoritedAmong(ctx context.Context, accountID int64, listingIDs []int64) (map[int64]bool, error)
	// AddFavorite is idempotent: saving twice is saving once.
	AddFavorite(ctx context.Context, accountID, listingID int64) error
	// RemoveFavorite answers no error when the row was not there — unsaving something that is
	// not saved is the state the caller asked for.
	RemoveFavorite(ctx context.Context, accountID, listingID int64) error
	// GetListingByVariant loads the aggregate a variant belongs to, scoped by owner: the
	// variant routes address the variant, but the rules live on the root.
	GetListingByVariant(ctx context.Context, variantID, sellerID int64) (*domain.Listing, error)

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
	// ReleaseStock gives them back — a cancelled or expired session. Only before the sale:
	// once the units are in `sold` the reversal is UncommitStock.
	ReleaseStock(ctx context.Context, variantID, units int64) error
	// CommitStock turns a reservation into a sale and bumps listing.cached_sold in the same
	// transaction, so the badge and the counter cannot drift apart. UncommitStock is its
	// reversal — a cancelled or refunded order.
	//
	// Both take a key, and both record it beside the counter change: `sold` never moves on
	// its own, so neither call is recoverable from the counters and a retry has to be refused
	// rather than reapplied.
	CommitStock(ctx context.Context, variantID, units int64, key string) error
	UncommitStock(ctx context.Context, variantID, units int64, key string) error
	FindStock(ctx context.Context, variantID int64) (domain.Stock, error)
	// SetCachedRating writes the review average trust recomputed. Denormalized here because
	// trust is another schema: the number cannot be joined, so it is handed over.
	SetCachedRating(ctx context.Context, listingID int64, rating float64, count int64) error
}
