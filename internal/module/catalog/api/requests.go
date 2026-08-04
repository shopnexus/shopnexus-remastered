package catalogapi

import (
	"time"

	"shopnexus/internal/shared/id"
)

// A field tagged `json:"-"` is filled by the gateway from the token or the path, never
// from the body: a request that could name its own actor is a request that can act as
// somebody else.
//
// An optional PATCH field is a pointer, and a nullable one carries a `clear_*` bool
// beside it: omitted leaves the field alone, a value replaces it, the flag removes it.

type ListCategoriesRequest struct {
	// Near ranks by closeness to these seeds instead of returning the tree. A seed is a
	// tag slug or a category id, told apart by the underscore an opaque id always has.
	Near  []string `json:"-" validate:"max=8,dive,required,max=100"`
	Limit int      `json:"-" validate:"required,min=1,max=50"`
}

type CreateCategoryRequest struct {
	ActorID     id.ID[id.Account]   `json:"-" validate:"required"`
	ParentID    *id.ID[id.Category] `json:"parent_id,omitempty"`
	Name        string              `json:"name" validate:"required,min=1,max=100"`
	Description string              `json:"description" validate:"max=2000"`
}

type UpdateCategoryRequest struct {
	ActorID       id.ID[id.Account]   `json:"-" validate:"required"`
	ID            id.ID[id.Category]  `json:"-" validate:"required"`
	ParentID      *id.ID[id.Category] `json:"parent_id,omitempty"`
	ClearParentID bool                `json:"clear_parent_id,omitempty"`
	Name          *string             `json:"name,omitempty" validate:"omitempty,min=1,max=100"`
	Description   *string             `json:"description,omitempty" validate:"omitempty,max=2000"`
}

type DeleteCategoryRequest struct {
	ActorID id.ID[id.Account]  `json:"-" validate:"required"`
	ID      id.ID[id.Category] `json:"-" validate:"required"`
}

// ListTagsRequest: Query is a prefix match on the slug, Near reorders the dictionary by
// meaning. They are mutually exclusive — one filters by what was typed, the other ranks by
// closeness, and combining them would rank a set the prefix already decided.
type ListTagsRequest struct {
	Query string   `json:"-" validate:"max=100"`
	Near  []string `json:"-" validate:"max=8,dive,required,max=100"`
	Page  int      `json:"-" validate:"required,min=1"`
	Limit int      `json:"-" validate:"required,min=1,max=100"`
}

type PutTagRequest struct {
	ActorID     id.ID[id.Account] `json:"-" validate:"required"`
	Slug        string            `json:"-" validate:"required,max=100"`
	Description *string           `json:"description,omitempty" validate:"omitempty,max=255"`
}

type DeleteTagRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	Slug    string            `json:"-" validate:"required,max=100"`
}

// StockMovementRequest is a service-to-service call from order: no route, no actor, because
// a reservation is not a user action — it is what a checkout does on the way past.
type StockMovementRequest struct {
	VariantID id.ID[id.Variant] `validate:"required"`
	Units     int64             `validate:"required,gt=0"`
}

// StockCommitRequest turns a reservation into a sale, or puts one back. Its own shape because
// of the key: a reservation that is released twice runs out of `reserved` and refuses itself,
// but `sold` carries no such guard, so a commit and its reversal each have to name the move
// they are — `order:41:item:88:commit` — and be applied once.
type StockCommitRequest struct {
	VariantID      id.ID[id.Variant] `validate:"required"`
	Units          int64             `validate:"required,gt=0"`
	IdempotencyKey string            `validate:"required,max=200"`
}

// CreateUploadRequest asks for a slot to PUT a listing photo into. The bytes never pass through
// the API: the answer is a short-lived signed URL, and a second call confirms the row once the
// object is there — so a listing can never render a photo whose bytes never arrived.
type CreateUploadRequest struct {
	ActorID  id.ID[id.Account] `json:"-" validate:"required"`
	Filename string            `json:"filename" validate:"required,max=255"`
	// Mime and Size are what the client is about to send. Both are checked before a byte
	// moves: a slot signed for anything is a slot for anything.
	Mime string `json:"mime" validate:"required,max=100"`
	Size int64  `json:"size" validate:"required,gt=0"`
}

// UploadSlot is where to PUT, what to confirm afterwards, and until when.
type UploadSlot struct {
	ResourceID id.ID[id.Resource] `json:"resource_id"`
	URL        string             `json:"url"`
	// Headers the client must send with the PUT, when the signature covers any.
	Headers   map[string]string `json:"headers,omitempty"`
	ExpiresAt time.Time         `json:"expires_at"`
}

// ConfirmUploadRequest is the second step. The size is read from the store rather than taken
// from the client, so what it declared cannot become the record.
type ConfirmUploadRequest struct {
	ActorID id.ID[id.Account]  `json:"-" validate:"required"`
	ID      id.ID[id.Resource] `json:"-" validate:"required"`
}

// SyncListingRatingRequest is trust pushing a recomputed review average into the cache
// catalog keeps. No route and no actor: reviews live in another schema, so the number cannot
// be joined and has to be handed over.
type SyncListingRatingRequest struct {
	ListingID id.ID[id.Listing] `validate:"required"`
	Rating    float64           `validate:"gte=0,lte=5"`
	Count     int64             `validate:"gte=0"`
}

// CreateVariantInput is one variant inside a create. It is not a request of its own: a
// listing is created with its variants, so there is no window in which it has nothing to
// sell.
type CreateVariantInput struct {
	Price          int64                `json:"price" validate:"required,gte=1"`
	Attributes     map[string]any       `json:"attributes" validate:"required,min=1"`
	PackageDetails map[string]any       `json:"package_details" validate:"required"`
	Attachments    []id.ID[id.Resource] `json:"attachments,omitempty" validate:"max=10"`
	Quantity       int64                `json:"quantity" validate:"gte=0"`
}

type CreateListingRequest struct {
	ActorID        id.ID[id.Account]    `json:"-" validate:"required"`
	Name           string               `json:"name" validate:"required,min=1,max=200"`
	Description    string               `json:"description" validate:"max=20000"`
	CategoryID     id.ID[id.Category]   `json:"category_id" validate:"required"`
	Condition      string               `json:"condition" validate:"required,oneof=new used damaged"`
	PriceMode      string               `json:"price_mode" validate:"required,oneof=fixed negotiable"`
	Currency       string               `json:"currency" validate:"required,len=3"`
	Specifications map[string]any       `json:"specifications,omitempty"`
	Attachments    []id.ID[id.Resource] `json:"attachments,omitempty" validate:"max=10"`
	Tags           []string             `json:"tags,omitempty" validate:"max=10,dive,required,max=100"`
	Variants       []CreateVariantInput `json:"variants" validate:"required,min=1,dive"`
}

// GetListingRequest carries the viewer so `favorited` can be answered without a second
// round trip. ViewerID is zero for an anonymous read.
type GetListingRequest struct {
	ID       id.ID[id.Listing] `json:"-" validate:"required"`
	ViewerID id.ID[id.Account] `json:"-"`
}

type CreateVariantRequest struct {
	ActorID   id.ID[id.Account] `json:"-" validate:"required"`
	ListingID id.ID[id.Listing] `json:"-" validate:"required"`
	CreateVariantInput
}

// UpdateVariantRequest: every field optional. quantity sets the total on hand; reserved and
// sold are not settable — checkout and cancellation move them.
type UpdateVariantRequest struct {
	ActorID        id.ID[id.Account]    `json:"-" validate:"required"`
	ID             id.ID[id.Variant]    `json:"-" validate:"required"`
	Price          *int64               `json:"price,omitempty" validate:"omitempty,gte=1"`
	Attributes     map[string]any       `json:"attributes,omitempty"`
	PackageDetails map[string]any       `json:"package_details,omitempty"`
	Attachments    []id.ID[id.Resource] `json:"attachments,omitempty" validate:"max=10"`
	Quantity       *int64               `json:"quantity,omitempty" validate:"omitempty,gte=0"`
	IsFeatured     *bool                `json:"is_featured,omitempty"`
}

type DeleteVariantRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	ID      id.ID[id.Variant] `json:"-" validate:"required"`
}

// UpdateListingRequest: every field optional. Variants are edited through their own routes,
// which is why none appear here — and why an edit to a live listing waits on moderation
// while a price change does not.
type UpdateListingRequest struct {
	ActorID                id.ID[id.Account]    `json:"-" validate:"required"`
	ID                     id.ID[id.Listing]    `json:"-" validate:"required"`
	Name                   *string              `json:"name,omitempty" validate:"omitempty,min=1,max=200"`
	Description            *string              `json:"description,omitempty" validate:"omitempty,max=20000"`
	CategoryID             *id.ID[id.Category]  `json:"category_id,omitempty"`
	Condition              *string              `json:"condition,omitempty" validate:"omitempty,oneof=new used damaged"`
	PriceMode              *string              `json:"price_mode,omitempty" validate:"omitempty,oneof=fixed negotiable"`
	Specifications         map[string]any       `json:"specifications,omitempty"`
	Attachments            []id.ID[id.Resource] `json:"attachments,omitempty" validate:"max=10"`
	Tags                   []string             `json:"tags,omitempty" validate:"max=10,dive,required,max=100"`
	FeaturedVariantID      *id.ID[id.Variant]   `json:"featured_variant_id,omitempty"`
	ClearFeaturedVariantID bool                 `json:"clear_featured_variant_id,omitempty"`
}

type DeleteListingRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	ID      id.ID[id.Listing] `json:"-" validate:"required"`
}

// PublishListingRequest sends a listing for review. PickupContactID is where a carrier collects
// this listing's goods — the seller picks one of their own saved addresses, which is also what the
// listing is filtered and measured by once it is live. Omitted means their default pickup address.
// SuggestListingRequest is the "photo in, listing out" input: the photos the seller already
// uploaded, and what they said about them — typed, spoken, or both. Nothing here is stored; the
// answer is a form the seller edits and posts through CreateListing.
type SuggestListingRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	// Attachments are confirmed uploads. The first three are what the model reads: past that a
	// phone's photos are the same object again, at real cost per image.
	Attachments []id.ID[id.Resource] `json:"attachments,omitempty" validate:"max=10"`
	// Note is what the seller typed. Optional, like everything else — but at least one of the
	// three has to be there, or there is nothing to look at.
	Note string `json:"note,omitempty" validate:"max=2000"`
	// VoiceNote is the recording itself, base64 in JSON. Inline rather than an upload because it
	// is input and not content: nothing keeps it, so nothing has to reap it.
	VoiceNote     []byte `json:"voice_note,omitempty"`
	VoiceNoteMime string `json:"voice_note_mime,omitempty" validate:"max=100"`
	// Language is an ISO-639-1 hint for the transcription, empty to let the model detect.
	Language string `json:"language,omitempty" validate:"omitempty,len=2"`
}

type PublishListingRequest struct {
	ActorID         id.ID[id.Account]  `json:"-" validate:"required"`
	ID              id.ID[id.Listing]  `json:"-" validate:"required"`
	PickupContactID *id.ID[id.Contact] `json:"pickup_contact_id,omitempty"`
}

type HideListingRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	ID      id.ID[id.Listing] `json:"-" validate:"required"`
}

// AdminListListingsRequest is the moderation queue. Status empty means everything awaiting a
// decision — a first publication or a held edit.
type AdminListListingsRequest struct {
	ActorID  id.ID[id.Account] `json:"-" validate:"required"`
	Status   string            `json:"-" validate:"omitempty,oneof=draft pending active hidden"`
	SellerID id.ID[id.Account] `json:"-"`
	Page     int               `json:"-" validate:"required,min=1"`
	Limit    int               `json:"-" validate:"required,min=1,max=100"`
}

type ApproveListingRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	ID      id.ID[id.Listing] `json:"-" validate:"required"`
	Note    string            `json:"note,omitempty" validate:"max=2000"`
}

type TakedownListingRequest struct {
	ActorID      id.ID[id.Account] `json:"-" validate:"required"`
	ID           id.ID[id.Listing] `json:"-" validate:"required"`
	Reason       string            `json:"reason" validate:"required,min=1,max=2000"`
	NotifySeller *bool             `json:"notify_seller,omitempty"`
}

// ListListingsRequest is the browse feed, the search, the wishlist page and the "resolve these
// ids" lookup — one query narrowed by parameters, because a wishlist wants exactly what a feed
// wants and a separate endpoint left the client resolving ids one by one.
type ListListingsRequest struct {
	// ViewerID is zero for an anonymous read. Mine, Favorited and Recommended need it.
	ViewerID id.ID[id.Account] `json:"-"`
	// IDs ignores every other filter.
	IDs []id.ID[id.Listing] `json:"-" validate:"max=100"`
	// Variants resolves the listings a set of variants belongs to, which is what a cart or
	// an order row needs: a variant is not addressable on its own here, so the listing it
	// hangs off is the only thing that can be rendered. Ignores the other filters too.
	Variants   []id.ID[id.Variant] `json:"-" validate:"max=100"`
	Query      string              `json:"-" validate:"max=200"`
	Mode       string              `json:"-" validate:"omitempty,oneof=lexical semantic hybrid"`
	Mine       bool                `json:"-"`
	Favorited  bool                `json:"-"`
	Status     string              `json:"-" validate:"omitempty,oneof=draft pending active hidden"`
	CategoryID *id.ID[id.Category] `json:"-"`
	Tag        string              `json:"-" validate:"max=100"`
	SellerID   *id.ID[id.Account]  `json:"-"`
	Condition  string              `json:"-" validate:"omitempty,oneof=new used damaged"`
	MinPrice   *int64              `json:"-" validate:"omitempty,gte=0"`
	MaxPrice   *int64              `json:"-" validate:"omitempty,gte=0"`
	// Where to look. The narrowest level the caller names is the filter — a ward is already
	// inside its province — and it is matched against the seller's pickup address as the listing
	// snapshotted it.
	ProvinceCode string `json:"-" validate:"max=20"`
	DistrictCode string `json:"-" validate:"max=20"`
	WardCode     string `json:"-" validate:"max=20"`
	// Where the buyer is, for a "near me" browse: either coordinates from the device, or one of
	// their own saved addresses. With a position every card carries its distance; RadiusKM bounds
	// the result to a circle, and `sort=distance` orders by it.
	Latitude      *float64           `json:"-" validate:"omitempty,gte=-90,lte=90"`
	Longitude     *float64           `json:"-" validate:"omitempty,gte=-180,lte=180"`
	NearContactID *id.ID[id.Contact] `json:"-"`
	RadiusKM      *float64           `json:"-" validate:"omitempty,gt=0,lte=500"`
	Sort          string             `json:"-" validate:"omitempty,oneof=newest rating price-asc price-desc best-selling relevance recommended distance"`
	Page          int                `json:"-" validate:"required,min=1"`
	Limit         int                `json:"-" validate:"required,min=1,max=100"`
}

// FavoriteRequest is one wishlist write. PUT and DELETE are both idempotent, so neither needs
// to know whether the row was already there.
type FavoriteRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	ID      id.ID[id.Listing] `json:"-" validate:"required"`
}
