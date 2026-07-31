package handler

import (
	"log/slog"
	"net/http"

	"github.com/go-playground/validator/v10"

	trustapi "shopnexus/internal/module/trust/api"
)

// Trust serves the trust module's routes: order feedback, product reviews, reputation and abuse reports.
//
// Scaffold. Every method answers 501 until it is written, and the routes are
// registered in router.go so the OpenAPI contract test can hold the two in step.
// The service, validator and logger are held already: it keeps the fx graph real —
// so the module's pool is opened and its config validated at startup — and makes
// filling a method in a local edit rather than a rewiring.
type Trust struct {
	svc trustapi.Service
	v   *validator.Validate
	log *slog.Logger
}

func NewTrust(svc trustapi.Service, v *validator.Validate, log *slog.Logger) *Trust {
	return &Trust{svc: svc, v: v, log: log}
}

// GetOrderFeedback handles GET /orders/{orderID}/feedback.
func (h *Trust) GetOrderFeedback(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// SubmitFeedback handles POST /orders/{orderID}/feedback.
func (h *Trust) SubmitFeedback(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// ListAccountFeedback handles GET /accounts/{accountID}/feedback.
func (h *Trust) ListAccountFeedback(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// GetReputation handles GET /accounts/{accountID}/reputation.
func (h *Trust) GetReputation(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// ListReviews handles GET /listings/{listingID}/reviews.
func (h *Trust) ListReviews(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// SubmitReview handles POST /listings/{listingID}/reviews.
func (h *Trust) SubmitReview(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// GetReview handles GET /reviews/{id}.
func (h *Trust) GetReview(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// UpdateReview handles PATCH /reviews/{id}.
func (h *Trust) UpdateReview(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// DeleteReview handles DELETE /reviews/{id}.
func (h *Trust) DeleteReview(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// SubmitReviewReply handles POST /reviews/{id}/replies.
func (h *Trust) SubmitReviewReply(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// DeleteReviewReply handles DELETE /review-replies/{id}.
func (h *Trust) DeleteReviewReply(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// VoteReview handles PUT /reviews/{id}/vote.
func (h *Trust) VoteReview(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// UnvoteReview handles DELETE /reviews/{id}/vote.
func (h *Trust) UnvoteReview(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// SubmitReport handles POST /reports.
func (h *Trust) SubmitReport(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// ListMyReports handles GET /reports.
func (h *Trust) ListMyReports(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AdminListReports handles GET /admin/reports.
func (h *Trust) AdminListReports(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AdminGetReport handles GET /admin/reports/{id}.
func (h *Trust) AdminGetReport(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AdminClaimReport handles POST /admin/reports/{id}/claim.
func (h *Trust) AdminClaimReport(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AdminResolveReport handles POST /admin/reports/{id}/resolution.
func (h *Trust) AdminResolveReport(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}
