package handler

import (
	"log/slog"
	"net/http"

	"github.com/go-playground/validator/v10"

	orderapi "shopnexus/internal/module/order/api"
)

// Order serves the order module's routes: the cart, purchase sessions, checkout, orders, offers, refunds and disputes.
//
// Scaffold. Every method answers 501 until it is written, and the routes are
// registered in router.go so the OpenAPI contract test can hold the two in step.
// The service, validator and logger are held already: it keeps the fx graph real —
// so the module's pool is opened and its config validated at startup — and makes
// filling a method in a local edit rather than a rewiring.
type Order struct {
	svc orderapi.Service
	v   *validator.Validate
	log *slog.Logger
}

func NewOrder(svc orderapi.Service, v *validator.Validate, log *slog.Logger) *Order {
	return &Order{svc: svc, v: v, log: log}
}

// ListCartItems handles GET /cart-items.
func (h *Order) ListCartItems(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AddCartItem handles POST /cart-items.
func (h *Order) AddCartItem(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// UpdateCartItem handles PATCH /cart-items/{id}.
func (h *Order) UpdateCartItem(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// DeleteCartItem handles DELETE /cart-items/{id}.
func (h *Order) DeleteCartItem(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// CreateDraft handles POST /drafts.
func (h *Order) CreateDraft(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// ListDrafts handles GET /drafts.
func (h *Order) ListDrafts(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// GetDraft handles GET /drafts/{id}.
func (h *Order) GetDraft(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// CancelDraft handles DELETE /drafts/{id}.
func (h *Order) CancelDraft(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// Checkout handles POST /drafts/{id}/checkout.
func (h *Order) Checkout(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// ListItems handles GET /items.
func (h *Order) ListItems(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// CancelItem handles POST /items/{id}/cancellation.
func (h *Order) CancelItem(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// ConfirmOrder handles POST /orders.
func (h *Order) ConfirmOrder(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// ListOrders handles GET /orders.
func (h *Order) ListOrders(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// GetOrder handles GET /orders/{id}.
func (h *Order) GetOrder(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// ConfirmReceipt handles POST /orders/{id}/receipt.
func (h *Order) ConfirmReceipt(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// CancelOrder handles POST /orders/{id}/cancellation.
func (h *Order) CancelOrder(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// GetOrderTransport handles GET /orders/{id}/transport.
func (h *Order) GetOrderTransport(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// CreateRefund handles POST /orders/{id}/refunds.
func (h *Order) CreateRefund(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// CreateOffer handles POST /offers.
func (h *Order) CreateOffer(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// ListOffers handles GET /offers.
func (h *Order) ListOffers(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// GetOffer handles GET /offers/{id}.
func (h *Order) GetOffer(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// CounterOffer handles PATCH /offers/{id}.
func (h *Order) CounterOffer(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// CancelOffer handles DELETE /offers/{id}.
func (h *Order) CancelOffer(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AcceptOffer handles POST /offers/{id}/acceptance.
func (h *Order) AcceptOffer(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// ListRefunds handles GET /refunds.
func (h *Order) ListRefunds(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// GetRefund handles GET /refunds/{id}.
func (h *Order) GetRefund(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// WithdrawRefund handles DELETE /refunds/{id}.
func (h *Order) WithdrawRefund(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AddRefundAttachments handles POST /refunds/{id}/attachments.
func (h *Order) AddRefundAttachments(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AcceptRefund handles POST /refunds/{id}/acceptance.
func (h *Order) AcceptRefund(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// RejectRefund handles POST /refunds/{id}/rejection.
func (h *Order) RejectRefund(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// GetDispute handles GET /disputes/{id}.
func (h *Order) GetDispute(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AddDisputeAttachments handles POST /disputes/{id}/attachments.
func (h *Order) AddDisputeAttachments(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AdminListDisputes handles GET /admin/disputes.
func (h *Order) AdminListDisputes(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AdminRuleDispute handles POST /admin/disputes/{id}/ruling.
func (h *Order) AdminRuleDispute(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}
