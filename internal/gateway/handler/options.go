package handler

import (
	"context"
	"log/slog"
	"net/http"

	"shopnexus/internal/module/common"
	financeapi "shopnexus/internal/module/finance/api"
	orderapi "shopnexus/internal/module/order/api"
	"shopnexus/internal/shared/httpx"
)

// Options serves the pluggable-integration registry: the payment rails and the carriers a client
// picks from, and the operator surface that switches them.
//
// One handler across modules, because there is one endpoint. The rows themselves stay in the schema
// of the module that acts on them — a settled payment names a rail and a shipped parcel names a
// carrier, so those two columns must be able to move databases with their module — and `category`
// is what says which module answers. That map is the only thing this file knows.
type Options struct {
	byCategory map[string]optionOwner
	log        *slog.Logger
}

// optionOwner is the pair of calls a module contributes. Its own interface rather than the module's
// whole api.Service: this handler has no business reaching anything else on them.
type optionOwner interface {
	ListOptions(ctx context.Context, req common.ListOptionsRequest) (common.OptionList, error)
	AdminSaveOption(ctx context.Context, req common.SaveOptionRequest) (common.OptionDTO, error)
}

func NewOptions(finance financeapi.Service, order orderapi.Service, log *slog.Logger) *Options {
	return &Options{
		byCategory: map[string]optionOwner{
			common.CategoryPayment:   finance,
			common.CategoryTransport: order,
		},
		log: log,
	}
}

// List handles GET /options?category=… — what a client may choose from.
func (h *Options) List(w http.ResponseWriter, r *http.Request) { h.list(w, r, false) }

// AdminList handles GET /admin/options?category=… — every row, disabled ones included, each with the
// provider serving it and the set an admin may move it to.
func (h *Options) AdminList(w http.ResponseWriter, r *http.Request) { h.list(w, r, true) }

func (h *Options) list(w http.ResponseWriter, r *http.Request, admin bool) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	category := r.URL.Query().Get("category")
	owner, err := h.owner(category)
	if failed(w, h.log, err) {
		return
	}
	res, err := owner.ListOptions(r.Context(), common.ListOptionsRequest{
		ActorID: uid, Category: category, Admin: admin,
	})
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// owner is which module holds a category's rows. An absent category is a 400 and an unknown one a
// 404 — the same 404 a category this caller may not see gets, since telling those apart would let
// anyone enumerate the platform's operator surface.
func (h *Options) owner(category string) (optionOwner, error) {
	if category == "" {
		return nil, common.ErrOptionCategoryRequired
	}
	owner, ok := h.byCategory[category]
	if !ok {
		return nil, common.ErrOptionCategoryUnknown
	}
	return owner, nil
}

// AdminSave handles PATCH /admin/options/{id} — switch a rail off, rename it, or move it to another
// implementation. The category is in the query because the id alone does not say which module holds
// the row, and there is no cross-schema place to look it up.
func (h *Options) AdminSave(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	owner, err := h.owner(r.URL.Query().Get("category"))
	if failed(w, h.log, err) {
		return
	}
	var req common.SaveOptionRequest
	if err := decodeBody(r, &req); failed(w, h.log, err) {
		return
	}
	req.ActorID, req.ID = uid, r.PathValue("id")
	res, err := owner.AdminSaveOption(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}
