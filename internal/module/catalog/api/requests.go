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
