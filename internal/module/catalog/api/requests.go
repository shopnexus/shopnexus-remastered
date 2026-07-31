package catalogapi

import "shopnexus/internal/shared/id"

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
	ShippingPaidBy string               `json:"shipping_paid_by" validate:"required,oneof=buyer seller"`
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
