package catalogapi

import (
	"time"

	accountapi "shopnexus/internal/module/account/api"
	commonapi "shopnexus/internal/module/common/api"
	"shopnexus/internal/shared/id"
)

// Category is one node of the browse tree. The client assembles the shape from
// ParentID, because a tree this small costs less to send flat than to nest.
type Category struct {
	ID          id.ID[id.Category]  `json:"id"`
	ParentID    *id.ID[id.Category] `json:"parent_id"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	// Score is set only by a `near` query, where the answer is a ranking rather than
	// the tree.
	Score *float64 `json:"score,omitempty"`
}

// Tag is a label on a listing. Its slug is its id — a natural key, so it is readable
// rather than encoded.
type Tag struct {
	Slug        string  `json:"slug"`
	Description *string `json:"description"`
	// Score is set only by a `near` query.
	Score *float64 `json:"score,omitempty"`
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
	Images         []commonapi.Resource `json:"images"`
	IsFeatured     bool                 `json:"is_featured"`
	Stock          Stock                `json:"stock"`
	CreatedAt      time.Time            `json:"created_at"`
}

// PendingEdit is an edit waiting on moderation — the editable subset of a listing, and only
// that: an edit that could carry anything would be a blob nobody can diff or review.
type PendingEdit struct {
	Name           *string              `json:"name,omitempty"`
	Description    *string              `json:"description,omitempty"`
	CategoryID     *id.ID[id.Category]  `json:"category_id,omitempty"`
	Condition      *string              `json:"condition,omitempty"`
	PriceMode      *string              `json:"price_mode,omitempty"`
	ShippingPaidBy *string              `json:"shipping_paid_by,omitempty"`
	Specifications map[string]any       `json:"specifications,omitempty"`
	Attachments    []id.ID[id.Resource] `json:"attachments,omitempty"`
	Tags           []string             `json:"tags,omitempty"`
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
	ShippingPaidBy    string                    `json:"shipping_paid_by"`
	Currency          string                    `json:"currency"`
	Specifications    map[string]any            `json:"specifications"`
	Images            []commonapi.Resource      `json:"images"`
	Category          Category                  `json:"category"`
	Tags              []string                  `json:"tags"`
	Variants          []Variant                 `json:"variants"`
	FeaturedVariantID *id.ID[id.Variant]        `json:"featured_variant_id"`
	Sold              int64                     `json:"sold"`
	Rating            float64                   `json:"rating"`
	Seller            accountapi.AccountSummary `json:"seller"`
	Favorited         bool                      `json:"favorited"`
	FavoriteCount     int64                     `json:"favorite_count"`
	PendingEdit       *PendingEdit              `json:"pending_edit"`
	CreatedAt         time.Time                 `json:"created_at"`
	DeletedAt         *time.Time                `json:"deleted_at"`
}

// Listing is the card a feed shows. Price is the featured variant's, or the cheapest one
// when a price sort is in force — not stored on the listing.
type Listing struct {
	ID         id.ID[id.Listing]         `json:"id"`
	Slug       string                    `json:"slug"`
	Name       string                    `json:"name"`
	Status     string                    `json:"status"`
	Condition  string                    `json:"condition"`
	PriceMode  string                    `json:"price_mode"`
	Currency   string                    `json:"currency"`
	Price      int64                     `json:"price"`
	Sold       int64                     `json:"sold"`
	Cover      *commonapi.Resource       `json:"cover"`
	Rating     float64                   `json:"rating"`
	CategoryID id.ID[id.Category]        `json:"category_id"`
	Seller     accountapi.AccountSummary `json:"seller"`
	Favorited  bool                      `json:"favorited"`
	Score      *float64                  `json:"score,omitempty"`
	DeletedAt  *time.Time                `json:"deleted_at"`
	CreatedAt  time.Time                 `json:"created_at"`
}

type ListingPage struct {
	Data []Listing `json:"data"`
	Meta PageInfo  `json:"meta"`
}
