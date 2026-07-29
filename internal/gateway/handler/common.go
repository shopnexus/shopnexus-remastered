package handler

import (
	"log/slog"
	"net/http"

	"github.com/go-playground/validator/v10"

	commonapi "shopnexus/internal/module/common/api"
)

// Common serves the common module's routes: file uploads and the integration registry.
//
// Scaffold. Every method answers 501 until it is written, and the routes are
// registered in router.go so the OpenAPI contract test can hold the two in step.
// The service, validator and logger are held already: it keeps the fx graph real —
// so the module's pool is opened and its config validated at startup — and makes
// filling a method in a local edit rather than a rewiring.
type Common struct {
	svc commonapi.Service
	v   *validator.Validate
	log *slog.Logger
}

func NewCommon(svc commonapi.Service, v *validator.Validate, log *slog.Logger) *Common {
	return &Common{svc: svc, v: v, log: log}
}

// CreateUpload handles POST /resources.
func (h *Common) CreateUpload(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// CompleteUpload handles POST /resources/{id}/completion.
func (h *Common) CompleteUpload(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// GetResource handles GET /resources/{id}.
func (h *Common) GetResource(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// DeleteResource handles DELETE /resources/{id}.
func (h *Common) DeleteResource(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// ListOptions handles GET /options.
func (h *Common) ListOptions(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// CreateOption handles POST /options.
func (h *Common) CreateOption(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// GetOption handles GET /options/{id}.
func (h *Common) GetOption(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// UpdateOption handles PATCH /options/{id}.
func (h *Common) UpdateOption(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// DeleteOption handles DELETE /options/{id}.
func (h *Common) DeleteOption(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AdminListOptions handles GET /admin/options.
func (h *Common) AdminListOptions(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}
