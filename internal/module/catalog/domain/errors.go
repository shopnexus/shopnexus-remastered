// Package domain: the catalog entities and the rules that hold whatever calls them.
package domain

import (
	"net/http"

	"shopnexus/internal/shared/errx"
)

// Catalog errors — every 4xx this module can produce. Not-found lives here so the
// postgres adapter can return it without importing the module root.
var (
	// --- authorization. The role is a row in the account module's table, so this module
	// asks that service for it; the codes match account's so a client sees one vocabulary.
	ErrAdminRequired     = errx.NewError(http.StatusForbidden, "admin_required", "admin role required")
	ErrModeratorRequired = errx.NewError(http.StatusForbidden, "moderator_required", "moderator role required")

	// --- categories ---
	ErrCategoryNotFound  = errx.NewError(http.StatusNotFound, "category_not_found", "category not found")
	ErrCategoryNameTaken = errx.NewError(http.StatusConflict, "category_name_taken", "a category with this name already exists")
	ErrCategoryInUse     = errx.NewError(http.StatusConflict, "category_in_use", "listings still reference this category")
	ErrCategoryCycle     = errx.NewError(http.StatusUnprocessableEntity, "category_cycle", "a category cannot be its own descendant")

	// --- tags ---
	ErrTagNotFound = errx.NewError(http.StatusNotFound, "tag_not_found", "tag not found")
	// ErrSeedNotEmbedded is a semantic seed whose vector the embedding pass has not written
	// yet. Rejected rather than dropped: ranking against the rest would answer a different
	// question than the one asked.
	ErrSeedNotEmbedded = errx.NewErrorf(http.StatusUnprocessableEntity, "seed_not_embedded", "seed %q has no embedding yet")

	// --- listings ---
	ErrListingNotFound = errx.NewError(http.StatusNotFound, "listing_not_found", "listing not found")
	// ErrVersionConflict is a save built on a stale read: somebody else changed the
	// listing in between, so a rule the root checked may no longer hold. 409 because
	// retrying the whole command is the right answer and only the caller knows whether
	// it still wants to.
	ErrVersionConflict       = errx.NewError(http.StatusConflict, "version_conflict", "this listing changed while you were editing it; try again")
	ErrSlugTaken             = errx.NewError(http.StatusConflict, "slug_taken", "a listing with this name already exists")
	ErrInvalidTransition     = errx.NewError(http.StatusConflict, "invalid_transition", "already live or already under moderation")
	ErrNotAwaitingModeration = errx.NewError(http.StatusConflict, "not_awaiting_moderation", "this listing has nothing awaiting moderation")
	ErrListingInUse          = errx.NewError(http.StatusConflict, "listing_in_use", "an open order still covers this listing")
	ErrNoVariant             = errx.NewError(http.StatusUnprocessableEntity, "no_variant", "a listing needs at least one variant with a price")
	ErrTooManyTags           = errx.NewError(http.StatusUnprocessableEntity, "too_many_tags", "a listing carries at most 10 tags")

	// --- variants ---
	ErrVariantNotFound = errx.NewError(http.StatusNotFound, "variant_not_found", "variant not found")
	// ErrDuplicateVariant is two live variants describing the same combination. The
	// partial unique index says it too; this is the answer a caller gets.
	ErrDuplicateVariant       = errx.NewError(http.StatusConflict, "duplicate_variant", "another live variant already has these attributes")
	ErrLastVariant            = errx.NewError(http.StatusConflict, "last_variant", "this is the only variant of a live listing")
	ErrQuantityBelowCommitted = errx.NewError(http.StatusUnprocessableEntity, "quantity_below_committed", "quantity is below what is already reserved or sold")
	ErrInsufficientStock      = errx.NewError(http.StatusConflict, "insufficient_stock", "not enough stock for this variant")
	// ErrFeaturedNotMine is a programmer error rather than a client one: the featured
	// variant is chosen from the listing's own set, and the schema cannot express any
	// other kind. 500 because the request was fine.
	ErrFeaturedNotMine = errx.NewError(http.StatusInternalServerError, "featured_not_mine", "the featured variant does not belong to this listing")
)
