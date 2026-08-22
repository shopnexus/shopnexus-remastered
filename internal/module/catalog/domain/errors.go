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
	ErrInvalidTransition     = errx.NewError(http.StatusConflict, "invalid_transition", "already live or already under moderation")
	ErrNotAwaitingModeration = errx.NewError(http.StatusConflict, "not_awaiting_moderation", "this listing has nothing awaiting moderation")
	ErrListingInUse          = errx.NewError(http.StatusConflict, "listing_in_use", "an open order still covers this listing")
	// ErrListingNotEmbedded is a listing the embedding pass has not reached, asked to serve as
	// the query of a "more like this" ranking. 409 rather than 404: the listing exists and the
	// answer is "not yet", which is a state that clears on its own.
	ErrListingNotEmbedded = errx.NewError(http.StatusConflict, "listing_not_embedded", "this listing has no embedding yet, so nothing can be ranked against it")
	// ErrNoPickupAddress is a seller publishing before they have said where a carrier collects.
	// Refused here rather than at the buyer's checkout, which is where it used to surface: the
	// listing was live, browsable and impossible to buy.
	// ErrAddressNotGeocoded is a "near me" browse from an address with no coordinates. Answered
	// rather than silently dropped: the client's move is to ask for the device's position.
	ErrAddressNotGeocoded = errx.NewError(http.StatusUnprocessableEntity, "address_not_geocoded", "that address has no coordinates, so it cannot measure distance")
	// ErrSuggestionEmpty is a suggestion asked for with no photo, no note and no voice note: there
	// is nothing to look at, and a form filled from nothing is a form of invention.
	ErrSuggestionEmpty = errx.NewError(http.StatusUnprocessableEntity, "suggestion_empty", "send at least a photo, a note or a voice note")
	// ErrVoiceNoteTooLarge is a recording that was left running. A seller describes an item in a
	// sentence or two.
	ErrVoiceNoteTooLarge = errx.NewError(http.StatusRequestEntityTooLarge, "voice_note_too_large", "that voice note is longer than this route accepts")
	// ErrVoiceNoteNotSupported is a voice note sent to a deployment whose model gateway has no
	// audio endpoint. Refused rather than dropped: the words a seller spoke are half of what
	// they told us, and a form filled from the photos alone would look like the model ignored
	// them.
	ErrVoiceNoteNotSupported = errx.NewError(http.StatusUnprocessableEntity, "voice_note_not_supported", "this deployment cannot transcribe a voice note; type the description instead")
	// ErrSuggestionUnusable is a model answer that cannot fill a form — malformed, or with no name
	// in it. 502, because nothing the caller sent is at fault and retrying is the fix.
	ErrSuggestionUnusable = errx.NewError(http.StatusBadGateway, "suggestion_unusable", "the model did not return a usable suggestion")
	// ErrChatQuestionsOwnListing is a seller asking for openers to send themselves. Chat refuses a
	// conversation with your own account, so the questions would have nowhere to go.
	ErrChatQuestionsOwnListing = errx.NewError(http.StatusUnprocessableEntity, "chat_questions_own_listing", "these are openers for a buyer; this listing is yours")
	// ErrChatQuestionsUnusable is a model answer with nothing renderable in it. 502 for the same
	// reason as ErrSuggestionUnusable, and the caller's move is its own fallback list rather than
	// a retry: a chat that opens without chips still opens.
	ErrChatQuestionsUnusable = errx.NewError(http.StatusBadGateway, "chat_questions_unusable", "the model did not return usable questions")
	ErrNoPickupAddress    = errx.NewError(http.StatusUnprocessableEntity, "no_pickup_address", "set a pickup address before publishing: it is where carriers collect and how buyers find you")
	ErrNoVariant          = errx.NewError(http.StatusUnprocessableEntity, "no_variant", "a listing needs at least one variant with a price")
	ErrTooManyTags        = errx.NewError(http.StatusUnprocessableEntity, "too_many_tags", "a listing carries at most 10 tags")

	// --- variants ---
	ErrVariantNotFound = errx.NewError(http.StatusNotFound, "variant_not_found", "variant not found")
	// ErrDuplicateVariant is two live variants describing the same combination. The
	// partial unique index says it too; this is the answer a caller gets.
	ErrDuplicateVariant = errx.NewError(http.StatusConflict, "duplicate_variant", "another live variant already has these attributes")
	ErrLastVariant      = errx.NewError(http.StatusConflict, "last_variant", "this is the only variant of a live listing")
	// ErrTooManyFeatured is two live variants both claiming the card. "duplicate_variant" used
	// to answer this, which told the caller about attributes it had not touched.
	ErrTooManyFeatured = errx.NewError(http.StatusConflict, "too_many_featured", "only one variant can be featured")
	// ErrNoFeatured is a listing whose card would have no price the seller picked. The flag is
	// moved, never dropped, so this is reachable only by asking to drop it.
	ErrNoFeatured             = errx.NewError(http.StatusConflict, "no_featured", "one variant must be featured")
	ErrQuantityBelowCommitted = errx.NewError(http.StatusUnprocessableEntity, "quantity_below_committed", "quantity is below what is already reserved or sold")
	ErrInsufficientStock      = errx.NewError(http.StatusConflict, "insufficient_stock", "not enough stock for this variant")
	// ErrStockMovementKeyRequired is a commit or a reversal with no idempotency key. 500
	// because it is a caller the validator should have refused: neither move is recoverable
	// from the counters, so applying one without a key is applying it an unknown number of times.
	ErrStockMovementKeyRequired = errx.NewError(http.StatusInternalServerError, "stock_movement_key_required", "a stock commit needs an idempotency key")

	// ErrIdentityRequired gates selling on the same flag the payout gate reads: finding out
	// after the first sale is worse than finding out now.
	ErrIdentityRequired   = errx.NewError(http.StatusUnprocessableEntity, "identity_required", "identity verification is required before selling")
	ErrAttachmentNotFound = errx.NewError(http.StatusNotFound, "attachment_not_found", "an image id names no confirmed resource")
	// ErrAuthenticationRequired is a filter about the caller — their own listings, their
	// wishlist, their recommendations — asked for without a token. 401 rather than an empty
	// page, because an empty wishlist and "we do not know who you are" are different answers.
	ErrAuthenticationRequired = errx.NewError(http.StatusUnauthorized, "authentication_required", "this filter is about the caller and needs a token")
)
