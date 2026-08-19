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
type ListingPage struct {
	Data       []Listing `json:"data"`
	Meta       PageInfo  `json:"meta"`
	Understood string    `json:"understood"`
	Probes     []string  `json:"probes"`
}
