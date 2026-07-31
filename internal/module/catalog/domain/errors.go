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
)
