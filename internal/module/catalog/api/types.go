package catalogapi

import (
	"time"

	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/common"
	"shopnexus/internal/shared/id"
)

// The price modes a listing is sold under, as the published `price_mode` reports them. Order
// branches on this value to decide which of the two sale paths is legal, so it is named here
// rather than spelled again by every consumer.
const (
	PriceModeFixed      = "fixed"
	PriceModeNegotiable = "negotiable"
)

// StatusActive is the one listing status anything may be sold from. The others are either not
// published yet — `draft`, `pending` — or down: `hidden` covers both a seller withdrawing
// their own and a moderator's takedown.
const StatusActive = "active"

// The interaction types a shopper's action against a listing is recorded under. Named here
// rather than in domain, because observability's popularity scorer reads the same vocabulary
// as weight-map keys without depending on this module's service.
//
// InteractionPurchase is the one type nobody submits through POST /listings/interactions — it
// is derived from order.OrderPlaced, never client-observed, so it stays out of
// RecordInteractionsRequest's validated set. It exists here anyway because it shares
// InteractionWeight and PositiveInteractionTypes with the six a client can send: a purchase is
// the strongest positive signal this platform has, and duplicating the map for one more entry
// would be the drift this file already exists to prevent.
const (
	InteractionView               = "view"
	InteractionClickFromSearch    = "click-from-search"
	InteractionClickFromRecommend = "click-from-recommended"
	InteractionClickFromCategory  = "click-from-category"
	InteractionNotInterested      = "not-interested"
	InteractionHidden             = "hidden"
	InteractionPurchase           = "purchase"
)

// InteractionWeight is how strongly one interaction counts, for the two things it feeds:
// how much it moves personalisation (only the positive five ever do — see
// PositiveInteractionTypes) and how much it moves the platform's popularity score, where a
// negative delta is exactly the point. A constant map, not config: like the feed's own
// FreshWeight and ExploreSharpness, retuning this is a code change, because a wrong value
// corrupts a weighted average rather than degrading a feature.
var InteractionWeight = map[string]float64{
	InteractionView:               0.2,
	InteractionClickFromSearch:    0.4,
	InteractionClickFromRecommend: 0.3,
	InteractionClickFromCategory:  0.3,
	InteractionNotInterested:      -0.6,
	InteractionHidden:             -1.0,
	InteractionPurchase:           0.8,
}

// PositiveInteractionTypes is the personalisation-eligible subset of InteractionWeight.
// NotInterested and Hidden are excluded on purpose: account_interest averages positive weights
// into a share of the page, and a negative one would corrupt that average rather than suppress
// anything — those two instead exclude a listing outright (see
// port.Repository.RecomputeInterests).
var PositiveInteractionTypes = []string{
	InteractionView, InteractionClickFromSearch, InteractionClickFromRecommend, InteractionClickFromCategory,
	InteractionPurchase,
}

// Category is one node of the browse tree. The client assembles the shape from
// ParentID, because a tree this small costs less to send flat than to nest.
type Category struct {
	ID          id.ID[id.Category]  `json:"id"`
	ParentID    *id.ID[id.Category] `json:"parent_id"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	// Score is set only by a `near` query, where the answer is a ranking rather than
	// the tree.
	Score *float64 `json:"score"`
}

// Tag is a label on a listing. Its slug is its id — a natural key, so it is readable
// rather than encoded.
type Tag struct {
	Slug        string  `json:"slug"`
	Description *string `json:"description"`
	// Score is set only by a `near` query.
	Score *float64 `json:"score"`
}

// PageInfo is the page-paginated meta every catalog list answers with. TotalCount is nil
// for a ranked query, where the top-K is all the search ever visited.
//
// Field for field identical to httpx.PageMeta, so a handler converts rather than maps:
// `httpx.PageMeta(res.Meta)`.
type PageInfo struct {
	Page       int    `json:"page"`
	Limit      int    `json:"limit"`
	TotalCount *int64 `json:"total_count"`
}

type TagPage struct {
	Data []Tag    `json:"data"`
	Meta PageInfo `json:"meta"`
}

// Stock is always read inside its own variant, so it does not repeat the variant id.
type Stock struct {
	Quantity int64 `json:"quantity"`
	Reserved int64 `json:"reserved"`
	Sold     int64 `json:"sold"`
	// Available is quantity − reserved − sold, computed rather than stored: three counters
	// and a derived one is one fact too many to keep in step.
	Available int64 `json:"available"`
}

// Variant is a purchasable variant. Price and package details live here rather than on the
// listing.
type Variant struct {
	ID             id.ID[id.Variant]    `json:"id"`
	Price          int64                `json:"price"`
	Attributes     map[string]any       `json:"attributes"`
	PackageDetails map[string]any       `json:"package_details"`
	Images         []common.ResourceDTO `json:"images"`
	IsFeatured     bool                 `json:"is_featured"`
	Stock          Stock                `json:"stock"`
	CreatedAt      time.Time            `json:"created_at"`
}

// PendingEdit is an edit waiting on moderation — the editable subset of a listing, and only
// that: an edit that could carry anything would be a blob nobody can diff or review.
type PendingEdit struct {
	Name           *string              `json:"name"`
	Description    *string              `json:"description"`
	CategoryID     *id.ID[id.Category]  `json:"category_id"`
	Condition      *string              `json:"condition"`
	PriceMode      *string              `json:"price_mode"`
	Specifications map[string]any       `json:"specifications"`
	Attachments    []id.ID[id.Resource] `json:"attachments"`
	Tags           []string             `json:"tags"`
}

// ListingDetail is the product page and the answer to every write: a variant has no read of
// its own, so editing one comes back as the refreshed listing.
type ListingDetail struct {
	ID                id.ID[id.Listing]         `json:"id"`
	Slug              string                    `json:"slug"`
	Name              string                    `json:"name"`
	Description       string                    `json:"description"`
	Status            string                    `json:"status"`
	Condition         string                    `json:"condition"`
	PriceMode         string                    `json:"price_mode"`
	Currency          string                    `json:"currency"`
	Specifications    map[string]any            `json:"specifications"`
	Images            []common.ResourceDTO      `json:"images"`
	Category          Category                  `json:"category"`
	Tags              []string                  `json:"tags"`
	Variants          []Variant                 `json:"variants"`
	FeaturedVariantID *id.ID[id.Variant]        `json:"featured_variant_id"`
	Sold              int64                     `json:"sold"`
	Rating            float64                   `json:"rating"`
	ReviewCount       int64                     `json:"review_count"`
	Seller            accountapi.AccountSummary `json:"seller"`
	Favorited         bool                      `json:"favorited"`
	FavoriteCount     int64                     `json:"favorite_count"`
	PendingEdit       *PendingEdit              `json:"pending_edit"`
	// TakenDownAt is set when staff removed the listing, and is what tells that apart from a
	// seller hiding their own — both read `hidden`. TakedownReason is what the moderator chose to
	// tell the seller, nil when they chose not to; the full reason stays in the audit trail.
	TakenDownAt    *time.Time `json:"taken_down_at"`
	TakedownReason *string    `json:"takedown_reason"`
	// Location is where the goods are, and nil on a listing that was never published: it is the
	// seller's pickup address, taken when they published.
	Location  *ListingLocation `json:"location"`
	CreatedAt time.Time        `json:"created_at"`
	DeletedAt *time.Time       `json:"deleted_at"`
}

// ForSale reports whether this listing may be bought right now.
//
// The read contract hands a hidden or soft-deleted listing to anyone, because a cart line and
// an order item both have to render one, and says that `status` and `deleted_at` are what
// report it cannot be bought. Nothing on the purchase path was reading them, so the promise
// held only as long as no caller tried: a taken-down listing could be added to a cart,
// negotiated over and checked out. This is that promise, in one place, for every path that
// spends money to ask.
func (l ListingDetail) ForSale() bool {
	return l.Status == StatusActive && l.DeletedAt == nil
}

// Listing is the card a feed shows. Price is the featured variant's, or the cheapest one
// when a price sort is in force — not stored on the listing.
type Listing struct {
	ID          id.ID[id.Listing]         `json:"id"`
	Slug        string                    `json:"slug"`
	Name        string                    `json:"name"`
	Status      string                    `json:"status"`
	Condition   string                    `json:"condition"`
	PriceMode   string                    `json:"price_mode"`
	Currency    string                    `json:"currency"`
	Price       int64                     `json:"price"`
	Sold        int64                     `json:"sold"`
	Cover       *common.ResourceDTO       `json:"cover"`
	Rating      float64                   `json:"rating"`
	ReviewCount int64                     `json:"review_count"`
	CategoryID  id.ID[id.Category]        `json:"category_id"`
	Seller      accountapi.AccountSummary `json:"seller"`
	Favorited   bool                      `json:"favorited"`
	Score       *float64                  `json:"score"`
	// Tags the listing carries, so a card renders its chips without a request of its own. Empty
	// rather than null for a listing with none.
	Tags []string `json:"tags"`
	// TakenDownAt lets a seller's own list mark which of their hidden listings staff removed. The
	// reason is on the detail read, since it is a sentence rather than a badge.
	TakenDownAt *time.Time `json:"taken_down_at"`
	// Location is where the goods are, and nil on a listing that was never published.
	Location  *ListingLocation `json:"location"`
	DeletedAt *time.Time       `json:"deleted_at"`
	CreatedAt time.Time        `json:"created_at"`
}

// ListingLocation is the seller's pickup address as the listing snapshotted it: the names a card
// shows and the codes a filter matches. A snapshot rather than a reference, because the address
// lives in another module and a listing has to keep saying where it was sold from.
// ListingSuggestion is a filled-in form, not a listing: every field is the model's proposal and the
// seller is expected to correct it. The optional ones are absent when it had nothing it could stand
// behind — an empty box the seller fills is better than a wrong value they have to notice.
type ListingSuggestion struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// CategoryID is null when nothing in this marketplace's tree fits what it saw.
	CategoryID *id.ID[id.Category] `json:"category_id"`
	// Condition is one of the listing conditions, or empty when it could not tell.
	Condition string   `json:"condition"`
	Tags      []string `json:"tags"`
	// Price is what the seller said, in the smallest currency unit — never an estimate. Null when
	// they did not say.
	Price *int64 `json:"price"`
	// WeightG is the parcel's estimated weight, which is what a shipping quote needs.
	WeightG        *int64         `json:"weight_g"`
	Specifications map[string]any `json:"specifications"`
	// Transcript is what the voice note was heard as, echoed so the seller can see why a field is
	// wrong rather than guess.
	Transcript string `json:"transcript"`
}

type ListingLocation struct {
	ProvinceCode string `json:"province_code"`
	ProvinceName string `json:"province_name"`
	// DistrictCode is null where the country has no district tier.
	DistrictCode *string `json:"district_code"`
	DistrictName *string `json:"district_name"`
	WardCode     string  `json:"ward_code"`
	WardName     string  `json:"ward_name"`
	// DistanceKM is how far the goods are from where the buyer said they are, and null unless they
	// said — or unless this address was never geocoded.
	DistanceKM *float64 `json:"distance_km"`
}

// ListingPage is one page shape for every listing query, ranked or not.
//
// Understood and Probes are what the search made of the shopper's words: the sentence to show
// them, and the phrases actually searched. Both are the zero value for a browse with no query —
// never a missing key.
// ListingHistoryEntry is one change the listing has been through, as its history reads it.
//
// The stored snapshot is not here: it is the whole row as it then was, and a client that
// rendered it would be reading a shape the database is free to change. What a history needs
// is what moved and who moved it.
type ListingHistoryEntry struct {
	// Version is the trail's own counter for this listing: 1 is the listing being created,
	// and it never repeats, so it is also the entry's key.
	Version    int64     `json:"version"`
	Code       string    `json:"code"`
	ChangeType string    `json:"change_type"`
	ChangedAt  time.Time `json:"changed_at"`
	// ActorKind says who was behind the change — `seller`, `staff` or `system` — and is what
	// a client renders when Actor is null. A moderator is `staff` to everyone; only staff
	// also get the account.
	ActorKind string `json:"actor_kind"`
	// Actor is the account responsible, and null when there is none to show: a change nobody
	// is responsible for, or a moderator's, read by anyone but staff.
	Actor *accountapi.AccountSummary `json:"actor"`
	// Fields is what an edit touched, in the listing's own field names. Empty for a fact that
	// names none — a publication, a takedown.
	Fields []string `json:"fields"`
	// Details is the rest of what was recorded: the status it reached, the variant it was
	// about, and — for staff only — the words a moderator wrote.
	Details map[string]any `json:"details"`
}

type ListingHistoryPage struct {
	Data []ListingHistoryEntry `json:"data"`
	Meta PageInfo              `json:"meta"`
}

// ShelfReason is why a shelf is on the page — the whole point of a shelf rather than a page of
// cards. Machine-readable rather than a sentence, because the sentence is the client's: every
// other enum on this API is localised there, and a Vietnamese title from the server would be
// the one string a second language could not translate.
//
// `interest` is one of the four slots a personalised feed ranks against, shown apart instead of
// blended. The feed answers "what is here for me" in one page; the shelves answer it four times
// and say which taste each answer came from, which is the difference between a good page and a
// page a reader can trust.
type ShelfReason = string

const (
	ReasonInterest         ShelfReason = "interest"
	ReasonBecauseYouViewed ShelfReason = "because-you-viewed"
	ReasonTrending         ShelfReason = "trending"
	ReasonBestSelling      ShelfReason = "best-selling"
	ReasonTopRated         ShelfReason = "top-rated"
	ReasonNewest           ShelfReason = "newest"
)

const (
	SubjectListing  = "listing"
	SubjectCategory = "category"
)

// ShelfSubject is what the reason is *about*: the listing you looked at, the category an
// interest slot turned out to be near. Null on a shelf whose reason is about nothing in
// particular — what is trending is trending for everyone.
//
// An interest is a direction in embedding space and has no name of its own, so the subject is
// the nearest category to it: that is the only honest label available, and it is the same
// ranking `GET /categories?near=` already answers with.
type ShelfSubject struct {
	// Kind is `listing` or `category`, and it fixes what ID means.
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ShelfBrowse is the search that widens a shelf into a whole page — what "see all" links to.
// Sent as parameters rather than as a URL: the client owns its own routes, and a server that
// wrote them would be writing the client's router.
type ShelfBrowse struct {
	Sort       string  `json:"sort"`
	CategoryID *string `json:"category_id"`
	SimilarTo  *string `json:"similar_to"`
}

// Shelf is one horizontal row of the home page: a reason, what it is about, the listings, and
// the browse behind it.
type Shelf struct {
	// Key is unique within the response — a reason can occur more than once (four interest
	// slots) and a client needs one stable handle per row.
	Key      string        `json:"key"`
	Reason   ShelfReason   `json:"reason"`
	Subject  *ShelfSubject `json:"subject"`
	Browse   ShelfBrowse   `json:"browse"`
	Listings []Listing     `json:"listings"`
}

// ShelfList is the whole home page's worth, in the order it should be read: what is about the
// reader first, then what is about the marketplace.
type ShelfList struct {
	Data []Shelf `json:"data"`
}

type ListingPage struct {
	Data       []Listing `json:"data"`
	Meta       PageInfo  `json:"meta"`
	Understood string    `json:"understood"`
	Probes     []string  `json:"probes"`
}
