package handler

import (
	"log/slog"
	"net/http"

	"github.com/go-playground/validator/v10"

	catalogapi "shopnexus/internal/module/catalog/api"
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

// CreateSku handles POST /listings/{id}/skus.
func (h *Catalog) CreateSku(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// UpdateSku handles PATCH /skus/{id}.
func (h *Catalog) UpdateSku(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// DeleteSku handles DELETE /skus/{id}.
func (h *Catalog) DeleteSku(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AddFavorite handles PUT /favorites/{spuID}.
func (h *Catalog) AddFavorite(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// RemoveFavorite handles DELETE /favorites/{spuID}.
func (h *Catalog) RemoveFavorite(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// ListCategories handles GET /categories.
func (h *Catalog) ListCategories(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// ListTags handles GET /tags.
func (h *Catalog) ListTags(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AdminCreateCategory handles POST /admin/categories.
func (h *Catalog) AdminCreateCategory(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AdminUpdateCategory handles PATCH /admin/categories/{id}.
func (h *Catalog) AdminUpdateCategory(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AdminDeleteCategory handles DELETE /admin/categories/{id}.
func (h *Catalog) AdminDeleteCategory(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AdminPutTag handles PUT /admin/tags/{slug}.
func (h *Catalog) AdminPutTag(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AdminDeleteTag handles DELETE /admin/tags/{slug}.
func (h *Catalog) AdminDeleteTag(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
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
