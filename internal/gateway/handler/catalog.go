package handler

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-playground/validator/v10"

	catalogapi "shopnexus/internal/module/catalog/api"
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

// ListListings handles GET /listings — browsing, search, the personalised feed, the
// caller's own drawer and resolving a batch of ids, all under one set of filters.
func (h *Catalog) ListListings(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// CreateListing handles POST /listings.
func (h *Catalog) CreateListing(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// GetListing handles GET /listings/{id}.
func (h *Catalog) GetListing(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// UpdateListing handles PATCH /listings/{id}.
func (h *Catalog) UpdateListing(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// DeleteListing handles DELETE /listings/{id}.
func (h *Catalog) DeleteListing(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// PublishListing handles POST /listings/{id}/publication.
func (h *Catalog) PublishListing(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// HideListing handles DELETE /listings/{id}/publication.
func (h *Catalog) HideListing(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// CreateVariant handles POST /listings/{id}/variants.
func (h *Catalog) CreateVariant(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// UpdateVariant handles PATCH /variants/{id}.
func (h *Catalog) UpdateVariant(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// DeleteVariant handles DELETE /variants/{id}.
func (h *Catalog) DeleteVariant(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AddFavorite handles PUT /favorites/{listingID}.
func (h *Catalog) AddFavorite(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// RemoveFavorite handles DELETE /favorites/{listingID}.
func (h *Catalog) RemoveFavorite(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
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

// AdminListListings handles GET /admin/listings.
func (h *Catalog) AdminListListings(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AdminApproveListing handles POST /admin/listings/{id}/approval.
func (h *Catalog) AdminApproveListing(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AdminTakedownListing handles POST /admin/listings/{id}/takedown.
func (h *Catalog) AdminTakedownListing(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}
