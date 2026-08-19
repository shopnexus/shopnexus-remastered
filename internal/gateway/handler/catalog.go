package handler

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-playground/validator/v10"

	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/module/common"
	"shopnexus/internal/shared/httpx"
	"shopnexus/internal/shared/id"
)

// Catalog serves the catalog module's routes: listings and their variants, categories,
// tags and listing moderation. A variant is part of the listing aggregate, so its
// writes answer with the whole listing and it has no read of its own.
//
// Scaffold. Every method answers 501 until it is written, and the routes are
// registered in router.go so the OpenAPI contract test can hold the two in step.
// The service, validator and logger are held already: it keeps the fx graph real —
// so the module's pool is opened and its config validated at startup — and makes
// filling a method in a local edit rather than a rewiring.
type Catalog struct {
	svc catalogapi.Service
	v   *validator.Validate
	log *slog.Logger
}

func NewCatalog(svc catalogapi.Service, v *validator.Validate, log *slog.Logger) *Catalog {
	return &Catalog{svc: svc, v: v, log: log}
}

// ListListings handles GET /listings — the feed, the search, the wishlist page and the
// "resolve these ids" lookup, all narrowing one query. Optional auth: the three filters that
// are about the caller need a token, and the service is what refuses them without one.
func (h *Catalog) ListListings(w http.ResponseWriter, r *http.Request) {
	page, limit, err := pageParams(r)
	if failed(w, h.log, err) {
		return
	}
	query := r.URL.Query()
	req := catalogapi.ListListingsRequest{
		Query:     query.Get("q"),
		Status:    query.Get("status"),
		Tag:       query.Get("tag"),
		Condition: query.Get("condition"),
		Sort:      query.Get("sort"),
		Seed:      query.Get("seed"),
		Page:      page,
		Limit:     limit,
	}
	if uid, err := actor(r); err == nil {
		req.ViewerID = uid
	}
	mine, err := boolParam(r, "mine")
	if failed(w, h.log, err) {
		return
	}
	if mine != nil {
		req.Mine = *mine
	}
	favorited, err := boolParam(r, "favorited")
	if failed(w, h.log, err) {
		return
	}
	if favorited != nil {
		req.Favorited = *favorited
	}
	for _, raw := range splitList(query.Get("ids")) {
		listingID, err := id.Parse[id.Listing](raw)
		if failed(w, h.log, err) {
			return
		}
		req.IDs = append(req.IDs, listingID)
	}
	if raw := query.Get("category_id"); raw != "" {
		categoryID, err := id.Parse[id.Category](raw)
		if failed(w, h.log, err) {
			return
		}
		req.CategoryID = &categoryID
	}
	if raw := query.Get("seller_id"); raw != "" {
		sellerID, err := id.Parse[id.Account](raw)
		if failed(w, h.log, err) {
			return
		}
		req.SellerID = &sellerID
	}
	if req.MinPrice, err = int64Param(r, "min_price"); failed(w, h.log, err) {
		return
	}
	if req.MaxPrice, err = int64Param(r, "max_price"); failed(w, h.log, err) {
		return
	}
	// Where to look, and where the buyer is looking from.
	req.ProvinceCode = query.Get("province_code")
	req.DistrictCode = query.Get("district_code")
	req.WardCode = query.Get("ward_code")
	if req.Latitude, err = floatParam(r, "lat"); failed(w, h.log, err) {
		return
	}
	if req.Longitude, err = floatParam(r, "lon"); failed(w, h.log, err) {
		return
	}
	if req.RadiusKM, err = floatParam(r, "radius_km"); failed(w, h.log, err) {
		return
	}
	if raw := query.Get("near_contact_id"); raw != "" {
		contactID, err := id.Parse[id.Contact](raw)
		if failed(w, h.log, err) {
			return
		}
		req.NearContactID = &contactID
	}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.ListListings(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	// The whole page, not data plus a pager: a search also answers what it took the query to
	// mean, and that is about the response rather than about any listing in it.
	httpx.WriteEnvelope(w, http.StatusOK, res)
}

// SuggestListing handles POST /listings/suggestions — "photo in, listing out". One synchronous
// call: the client shows a skeleton form while a vision model reads the photos, and fills it in
// from the answer. Nothing is stored, so an abandoned attempt leaves nothing behind.
func (h *Catalog) SuggestListing(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	var req catalogapi.SuggestListingRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID = uid
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.SuggestListing(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// CreateListing handles POST /listings — the listing and its variants in one call.
func (h *Catalog) CreateListing(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	var req catalogapi.CreateListingRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID = uid
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.CreateListing(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusCreated, res)
}

// GetListing handles GET /listings/{id}. Optional auth: a signed-in viewer also learns
// whether the listing is on their wishlist.
func (h *Catalog) GetListing(w http.ResponseWriter, r *http.Request) {
	// The read route takes either handle: the opaque id an order item references the listing
	// by, or the public slug a shared link carries. Only reads — a write addresses the
	// listing by id, so a stale link can never edit whatever now sits behind it.
	listingID, err := catalogapi.ParseListingRef(r.PathValue("id"))
	if failed(w, h.log, err) {
		return
	}
	req := catalogapi.GetListingRequest{ID: listingID}
	// actor() answers an error for an anonymous request, which is not one here: the route is
	// registered under optionalAuth precisely so a buyer need not sign in to read.
	if uid, err := actor(r); err == nil {
		req.ViewerID = uid
	}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.GetListing(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// UpdateListing handles PATCH /listings/{id}.
func (h *Catalog) UpdateListing(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	listingID, err := pathID[id.Listing](r, "id")
	if failed(w, h.log, err) {
		return
	}
	var req catalogapi.UpdateListingRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID, req.ID = uid, listingID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.UpdateListing(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	// 202 when the change is waiting on a moderator, 200 when it landed. The body says which
	// either way; the status saves a client from having to read it to find out.
	if res.PendingEdit != nil {
		httpx.WriteData(w, http.StatusAccepted, res)
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// DeleteListing handles DELETE /listings/{id} — soft, so order history stays resolvable.
func (h *Catalog) DeleteListing(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	listingID, err := pathID[id.Listing](r, "id")
	if failed(w, h.log, err) {
		return
	}
	req := catalogapi.DeleteListingRequest{ActorID: uid, ID: listingID}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	if failed(w, h.log, h.svc.DeleteListing(r.Context(), req)) {
		return
	}
	httpx.WriteNoContent(w)
}

// PublishListing handles POST /listings/{id}/publication. 202, not 200: it queues the
// listing for moderation and nothing is live yet.
func (h *Catalog) PublishListing(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	listingID, err := pathID[id.Listing](r, "id")
	if failed(w, h.log, err) {
		return
	}
	// The body is optional: it names which of the seller's addresses a carrier collects from, and
	// leaving it out means their default.
	var req catalogapi.PublishListingRequest
	if failed(w, h.log, decodeOptionalBody(r, &req)) {
		return
	}
	req.ActorID, req.ID = uid, listingID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.PublishListing(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusAccepted, res)
}

// HideListing handles DELETE /listings/{id}/publication — the seller taking their own listing
// down. Publishing it again re-enters moderation.
func (h *Catalog) HideListing(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	listingID, err := pathID[id.Listing](r, "id")
	if failed(w, h.log, err) {
		return
	}
	req := catalogapi.HideListingRequest{ActorID: uid, ID: listingID}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.HideListing(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// CreateVariant handles POST /listings/{id}/variants.
func (h *Catalog) CreateVariant(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	listingID, err := pathID[id.Listing](r, "id")
	if failed(w, h.log, err) {
		return
	}
	var req catalogapi.CreateVariantRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID, req.ListingID = uid, listingID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.CreateVariant(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusCreated, res)
}

// UpdateVariant handles PATCH /variants/{id}.
func (h *Catalog) UpdateVariant(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	variantID, err := pathID[id.Variant](r, "id")
	if failed(w, h.log, err) {
		return
	}
	var req catalogapi.UpdateVariantRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID, req.ID = uid, variantID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.UpdateVariant(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// DeleteVariant handles DELETE /variants/{id}. Answers the listing rather than an empty
// body: deleting the featured variant moves the flag, and the seller has to see that.
func (h *Catalog) DeleteVariant(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	variantID, err := pathID[id.Variant](r, "id")
	if failed(w, h.log, err) {
		return
	}
	req := catalogapi.DeleteVariantRequest{ActorID: uid, ID: variantID}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.DeleteVariant(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// RecordInteractions handles POST /listings/interactions — a batch of shopper actions against
// listings. Optional auth: an anonymous view still counts toward popularity, and only a
// signed-in one moves personalisation.
func (h *Catalog) RecordInteractions(w http.ResponseWriter, r *http.Request) {
	var req catalogapi.RecordInteractionsRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	if uid, err := actor(r); err == nil {
		req.ActorID = uid
	}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	if failed(w, h.log, h.svc.RecordInteractions(r.Context(), req)) {
		return
	}
	httpx.WriteNoContent(w)
}

// AddFavorite handles PUT /favorites/{listingID}. Idempotent, so saving twice answers the same.
func (h *Catalog) AddFavorite(w http.ResponseWriter, r *http.Request) {
	req, err := h.favoriteRequest(r)
	if failed(w, h.log, err) {
		return
	}
	if failed(w, h.log, h.svc.AddFavorite(r.Context(), req)) {
		return
	}
	httpx.WriteNoContent(w)
}

// RemoveFavorite handles DELETE /favorites/{listingID}. Also idempotent: unsaving what is not
// saved leaves the caller where they wanted to be.
func (h *Catalog) RemoveFavorite(w http.ResponseWriter, r *http.Request) {
	req, err := h.favoriteRequest(r)
	if failed(w, h.log, err) {
		return
	}
	if failed(w, h.log, h.svc.RemoveFavorite(r.Context(), req)) {
		return
	}
	httpx.WriteNoContent(w)
}

// favoriteRequest reads the two things both wishlist writes need.
func (h *Catalog) favoriteRequest(r *http.Request) (catalogapi.FavoriteRequest, error) {
	uid, err := actor(r)
	if err != nil {
		return catalogapi.FavoriteRequest{}, err
	}
	listingID, err := pathID[id.Listing](r, "listingID")
	if err != nil {
		return catalogapi.FavoriteRequest{}, err
	}
	req := catalogapi.FavoriteRequest{ActorID: uid, ID: listingID}
	return req, check(h.v, req)
}

// ListCategories handles GET /categories — the tree, or a `near` ranking.
func (h *Catalog) ListCategories(w http.ResponseWriter, r *http.Request) {
	limit, err := intParam(r, "limit", 10, 1, 50)
	if failed(w, h.log, err) {
		return
	}
	req := catalogapi.ListCategoriesRequest{Limit: limit}
	if raw := r.URL.Query().Get("near"); raw != "" {
		req.Near = strings.Split(raw, ",")
	}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.ListCategories(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// ListTags handles GET /tags.
func (h *Catalog) ListTags(w http.ResponseWriter, r *http.Request) {
	page, limit, err := pageParams(r)
	if failed(w, h.log, err) {
		return
	}
	req := catalogapi.ListTagsRequest{Query: r.URL.Query().Get("q"), Page: page, Limit: limit}
	if raw := r.URL.Query().Get("near"); raw != "" {
		req.Near = strings.Split(raw, ",")
	}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.ListTags(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WritePage(w, http.StatusOK, res.Data, httpx.PageMeta(res.Meta))
}

// AdminCreateCategory handles POST /admin/categories.
func (h *Catalog) AdminCreateCategory(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	var req catalogapi.CreateCategoryRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID = uid
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.AdminCreateCategory(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusCreated, res)
}

// AdminUpdateCategory handles PATCH /admin/categories/{id}.
func (h *Catalog) AdminUpdateCategory(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	categoryID, err := pathID[id.Category](r, "id")
	if failed(w, h.log, err) {
		return
	}
	var req catalogapi.UpdateCategoryRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID, req.ID = uid, categoryID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.AdminUpdateCategory(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// AdminDeleteCategory handles DELETE /admin/categories/{id}.
func (h *Catalog) AdminDeleteCategory(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	categoryID, err := pathID[id.Category](r, "id")
	if failed(w, h.log, err) {
		return
	}
	req := catalogapi.DeleteCategoryRequest{ActorID: uid, ID: categoryID}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	if failed(w, h.log, h.svc.AdminDeleteCategory(r.Context(), req)) {
		return
	}
	httpx.WriteNoContent(w)
}

// AdminPutTag handles PUT /admin/tags/{slug}.
func (h *Catalog) AdminPutTag(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	var req catalogapi.PutTagRequest
	// Only the description can change, so a body-less PUT is a legal way to clear it.
	if failed(w, h.log, decodeOptionalBody(r, &req)) {
		return
	}
	// The slug is a natural key, so it comes off the path as a plain string — never parsed
	// as an opaque id.
	req.ActorID, req.Slug = uid, r.PathValue("slug")
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.AdminPutTag(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// AdminDeleteTag handles DELETE /admin/tags/{slug}.
func (h *Catalog) AdminDeleteTag(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	req := catalogapi.DeleteTagRequest{ActorID: uid, Slug: r.PathValue("slug")}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	if failed(w, h.log, h.svc.AdminDeleteTag(r.Context(), req)) {
		return
	}
	httpx.WriteNoContent(w)
}

// AdminListListings handles GET /admin/listings — the moderation queue.
func (h *Catalog) AdminListListings(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	page, limit, err := pageParams(r)
	if failed(w, h.log, err) {
		return
	}
	req := catalogapi.AdminListListingsRequest{
		ActorID: uid,
		Status:  r.URL.Query().Get("status"),
		Page:    page,
		Limit:   limit,
	}
	if raw := r.URL.Query().Get("seller_id"); raw != "" {
		sellerID, err := id.Parse[id.Account](raw)
		if failed(w, h.log, err) {
			return
		}
		req.SellerID = sellerID
	}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.AdminListListings(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WritePage(w, http.StatusOK, res.Data, httpx.PageMeta(res.Meta))
}

// AdminApproveListing handles POST /admin/listings/{id}/approval.
func (h *Catalog) AdminApproveListing(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	listingID, err := pathID[id.Listing](r, "id")
	if failed(w, h.log, err) {
		return
	}
	var req catalogapi.ApproveListingRequest
	// The note is optional, so the body may be absent entirely.
	if failed(w, h.log, decodeOptionalBody(r, &req)) {
		return
	}
	req.ActorID, req.ID = uid, listingID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.AdminApproveListing(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// AdminTakedownListing handles POST /admin/listings/{id}/takedown.
func (h *Catalog) AdminTakedownListing(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	listingID, err := pathID[id.Listing](r, "id")
	if failed(w, h.log, err) {
		return
	}
	var req catalogapi.TakedownListingRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID, req.ID = uid, listingID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.AdminTakedownListing(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// CreateUpload handles POST /listings/uploads — a slot to PUT a listing photo into.
func (h *Catalog) CreateUpload(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	var req common.CreateUploadRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID = uid
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.CreateUpload(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusCreated, res)
}

// ConfirmUpload handles POST /listings/uploads/{id}/confirmation — the bytes are at the store.
func (h *Catalog) ConfirmUpload(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	resourceID, err := pathID[id.Resource](r, "id")
	if failed(w, h.log, err) {
		return
	}
	req := common.ConfirmUploadRequest{ActorID: uid, ID: resourceID}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.ConfirmUpload(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}
