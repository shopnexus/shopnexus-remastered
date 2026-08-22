// Package catalogapi is the published contract of the catalog service.
//
// Other modules and the gateway depend on this, never on the service package. Methods
// are added one slice at a time, matching api/openapi/*.yaml.
package catalogapi

import (
	"context"

	"shopnexus/internal/module/common"
)

type Service interface {
	// --- categories: the browse tree. Reading is public, writing is admin-only.
	ListCategories(ctx context.Context, req ListCategoriesRequest) ([]Category, error)
	AdminCreateCategory(ctx context.Context, req CreateCategoryRequest) (Category, error)
	AdminUpdateCategory(ctx context.Context, req UpdateCategoryRequest) (Category, error)
	AdminDeleteCategory(ctx context.Context, req DeleteCategoryRequest) error

	// --- tags: reading is public, writing is admin-only ---
	ListTags(ctx context.Context, req ListTagsRequest) (TagPage, error)
	AdminPutTag(ctx context.Context, req PutTagRequest) (Tag, error)
	AdminDeleteTag(ctx context.Context, req DeleteTagRequest) error

	// --- listings: the seller's write model. Every write answers with the whole listing,
	// because a variant has no read of its own.
	// ListListings is the feed, the search, the wishlist page and the id lookup. Cards, not
	// aggregates: a page of twenty must not be twenty loads.
	ListListings(ctx context.Context, req ListListingsRequest) (ListingPage, error)
	// ListShelves is the home page: several short, reason-carrying rows instead of one ranked
	// page. Only the server can compose it — the interest slots it decomposes are not published.
	ListShelves(ctx context.Context, req ListShelvesRequest) (ShelfList, error)
	// SuggestListing fills in a listing form from the seller's photos and what they said about
	// them. It writes nothing: the answer is a suggestion the seller edits and then posts through
	// CreateListing, so no model's guess reaches a buyer without a human between.
	SuggestListing(ctx context.Context, req SuggestListingRequest) (ListingSuggestion, error)
	// SuggestChatQuestions answers the openers a buyer would tap to start a chat about a listing.
	// Read-only and per-call: they are drawn from what the listing already says, so that what
	// comes back leaves out the questions the page has answered.
	SuggestChatQuestions(ctx context.Context, req SuggestChatQuestionsRequest) (ChatQuestions, error)
	CreateListing(ctx context.Context, req CreateListingRequest) (ListingDetail, error)
	GetListing(ctx context.Context, req GetListingRequest) (ListingDetail, error)

	// A variant write answers with the whole listing: deleting the featured one moves the
	// flag, and a response carrying the variant alone could not report that.
	CreateVariant(ctx context.Context, req CreateVariantRequest) (ListingDetail, error)
	UpdateVariant(ctx context.Context, req UpdateVariantRequest) (ListingDetail, error)
	DeleteVariant(ctx context.Context, req DeleteVariantRequest) (ListingDetail, error)

	UpdateListing(ctx context.Context, req UpdateListingRequest) (ListingDetail, error)
	// ListListingHistory is the listing's own trail — what changed, when, and who was
	// behind it. Its two readers are the seller who owns the listing and staff, and they
	// are not answered the same rows: a moderator's identity and the words they wrote for
	// each other are staff's, not the seller's.
	ListListingHistory(ctx context.Context, req ListListingHistoryRequest) (ListingHistoryPage, error)
	DeleteListing(ctx context.Context, req DeleteListingRequest) error
	// PublishListing always enters moderation: there is no path that makes a listing live
	// without a human, which is also why re-publishing a taken-down listing cannot undo the
	// takedown.
	PublishListing(ctx context.Context, req PublishListingRequest) (ListingDetail, error)
	HideListing(ctx context.Context, req HideListingRequest) (ListingDetail, error)

	// --- uploads: a listing photo, in two steps ---
	// CreateUpload reserves a row and a presigned slot; ConfirmUpload makes it real once the
	// bytes are at the store. Until then the resource resolves to nothing, so a half-finished
	// upload cannot be attached to anything.
	CreateUpload(ctx context.Context, req common.CreateUploadRequest) (common.UploadSlotDTO, error)
	ConfirmUpload(ctx context.Context, req common.ConfirmUploadRequest) (common.ResourceDTO, error)

	// --- wishlist: both idempotent, so a retry is the state the caller asked for ---
	AddFavorite(ctx context.Context, req FavoriteRequest) error
	RemoveFavorite(ctx context.Context, req FavoriteRequest) error

	// RecordInteractions publishes a batch of shopper actions and returns as soon as they
	// are queued — nothing downstream (popularity, personalisation) is on the request's
	// critical path.
	RecordInteractions(ctx context.Context, req RecordInteractionsRequest) error

	// --- moderation: moderator or admin only ---
	AdminListListings(ctx context.Context, req AdminListListingsRequest) (ListingPage, error)
	// AdminApproveListing clears whatever was awaiting a decision: a first publication, or an
	// edit held against a live listing.
	AdminApproveListing(ctx context.Context, req ApproveListingRequest) (ListingDetail, error)
	AdminTakedownListing(ctx context.Context, req TakedownListingRequest) (ListingDetail, error)

	// --- stock: called by order, not by a route ---
	// ReserveStock holds units for a checkout that has not completed. Answers
	// 409 insufficient_stock when there is no room, which the caller acts on.
	ReserveStock(ctx context.Context, req StockMovementRequest) error
	// ReleaseStock gives a reservation back. Only before the sale: after CommitStock the
	// units are in `sold`, and the reversal is UncommitStock.
	ReleaseStock(ctx context.Context, req StockMovementRequest) error
	// CommitStock turns a reservation into a sale; UncommitStock puts one back on the shelf
	// when the order is cancelled or refunded. Both carry an idempotency key, because neither
	// is recoverable from the counters afterwards.
	CommitStock(ctx context.Context, req StockCommitRequest) error
	UncommitStock(ctx context.Context, req StockCommitRequest) error

	// --- the review cache: called by trust, not by a route ---
	// SyncListingRating writes the average and the count trust recomputed. Best-effort by
	// design on the caller's side: a cached number that lags is repaired by the next write.
	SyncListingRating(ctx context.Context, req SyncListingRatingRequest) error
}
