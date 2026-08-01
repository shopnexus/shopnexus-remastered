package handler

import (
	"log/slog"
	"net/http"

	"github.com/go-playground/validator/v10"

	trustapi "shopnexus/internal/module/trust/api"
	"shopnexus/internal/shared/httpx"
	"shopnexus/internal/shared/id"
)

// Trust serves the trust module's routes: order feedback, product reviews with their replies
// and votes, reputation, and abuse reports with a moderator surface.
//
// Cursor-paginated throughout, because every list here moves under the reader: a review gains
// votes, a queue is worked, a blind rating is revealed.
type Trust struct {
	svc trustapi.Service
	v   *validator.Validate
	log *slog.Logger
}

func NewTrust(svc trustapi.Service, v *validator.Validate, log *slog.Logger) *Trust {
	return &Trust{svc: svc, v: v, log: log}
}

// ---------------------------------------------------------------- feedback ---

// GetOrderFeedback handles GET /orders/{orderID}/feedback.
func (h *Trust) GetOrderFeedback(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	orderID, err := pathID[id.Order](r, "orderID")
	if failed(w, h.log, err) {
		return
	}
	req := trustapi.OrderFeedbackRequest{ActorID: uid, OrderID: orderID}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.GetOrderFeedback(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// SubmitFeedback handles POST /orders/{orderID}/feedback.
func (h *Trust) SubmitFeedback(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	orderID, err := pathID[id.Order](r, "orderID")
	if failed(w, h.log, err) {
		return
	}
	var req trustapi.SubmitFeedbackRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID, req.OrderID = uid, orderID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.SubmitFeedback(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusCreated, res)
}

// ListAccountFeedback handles GET /accounts/{accountID}/feedback.
func (h *Trust) ListAccountFeedback(w http.ResponseWriter, r *http.Request) {
	accountID, err := pathID[id.Account](r, "accountID")
	if failed(w, h.log, err) {
		return
	}
	limit, err := limitParam(r)
	if failed(w, h.log, err) {
		return
	}
	req := trustapi.ListFeedbackRequest{
		AccountID: accountID,
		Role:      r.URL.Query().Get("role"),
		Cursor:    cursorParam(r),
		Limit:     limit,
	}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.ListAccountFeedback(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteCursor(w, http.StatusOK, res.Data, cursorMeta(res.Meta.NextCursor))
}

// GetReputation handles GET /accounts/{accountID}/reputation. The role defaults to seller,
// which is what a profile page shows.
func (h *Trust) GetReputation(w http.ResponseWriter, r *http.Request) {
	accountID, err := pathID[id.Account](r, "accountID")
	if failed(w, h.log, err) {
		return
	}
	role := stringParam(r, "role", trustapi.RoleSeller)
	req := trustapi.GetReputationRequest{AccountID: accountID, Role: role}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.GetReputation(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// ----------------------------------------------------------------- reviews ---

// ListReviews handles GET /listings/{listingID}/reviews. Public, and a signed-in caller also
// gets their own vote on each row.
func (h *Trust) ListReviews(w http.ResponseWriter, r *http.Request) {
	listingID, err := pathID[id.Listing](r, "listingID")
	if failed(w, h.log, err) {
		return
	}
	limit, err := limitParam(r)
	if failed(w, h.log, err) {
		return
	}
	rating, err := intParam(r, "rating", 0, 1, 5)
	if failed(w, h.log, err) {
		return
	}
	req := trustapi.ListReviewsRequest{
		ListingID: listingID,
		Rating:    int16(rating),
		Sort:      r.URL.Query().Get("sort"),
		Cursor:    cursorParam(r),
		Limit:     limit,
	}
	// actor() answers an error for an anonymous request, which is not one here: the route is
	// under optionalAuth precisely so a reader need not sign in.
	if uid, err := actor(r); err == nil {
		req.ViewerID = uid
	}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.ListReviews(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteCursor(w, http.StatusOK, res.Data, cursorMeta(res.Meta.NextCursor))
}

// SubmitReview handles POST /listings/{listingID}/reviews.
func (h *Trust) SubmitReview(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	listingID, err := pathID[id.Listing](r, "listingID")
	if failed(w, h.log, err) {
		return
	}
	var req trustapi.SubmitReviewRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID, req.ListingID = uid, listingID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.SubmitReview(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusCreated, res)
}

// GetReview handles GET /reviews/{id}. Carries the whole reply thread, unlike the listing
// page, which caps it.
func (h *Trust) GetReview(w http.ResponseWriter, r *http.Request) {
	reviewID, err := pathID[id.Review](r, "id")
	if failed(w, h.log, err) {
		return
	}
	req := trustapi.GetReviewRequest{ID: reviewID}
	if uid, err := actor(r); err == nil {
		req.ViewerID = uid
	}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.GetReview(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// UpdateReview handles PATCH /reviews/{id}.
func (h *Trust) UpdateReview(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	reviewID, err := pathID[id.Review](r, "id")
	if failed(w, h.log, err) {
		return
	}
	var req trustapi.UpdateReviewRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID, req.ID = uid, reviewID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.UpdateReview(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// DeleteReview handles DELETE /reviews/{id}.
func (h *Trust) DeleteReview(w http.ResponseWriter, r *http.Request) {
	req, ok := h.reviewRequest(w, r)
	if !ok {
		return
	}
	if failed(w, h.log, h.svc.DeleteReview(r.Context(), req)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SubmitReviewReply handles POST /reviews/{id}/replies.
func (h *Trust) SubmitReviewReply(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	reviewID, err := pathID[id.Review](r, "id")
	if failed(w, h.log, err) {
		return
	}
	var req trustapi.SubmitReplyRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID, req.ReviewID = uid, reviewID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.SubmitReply(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusCreated, res)
}

// DeleteReviewReply handles DELETE /review-replies/{id}.
func (h *Trust) DeleteReviewReply(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	replyID, err := pathID[id.ReviewReply](r, "id")
	if failed(w, h.log, err) {
		return
	}
	req := trustapi.ReplyRequest{ActorID: uid, ID: replyID}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	if failed(w, h.log, h.svc.DeleteReply(r.Context(), req)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// VoteReview handles PUT /reviews/{id}/vote.
func (h *Trust) VoteReview(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	reviewID, err := pathID[id.Review](r, "id")
	if failed(w, h.log, err) {
		return
	}
	var req trustapi.VoteReviewRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID, req.ID = uid, reviewID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.VoteReview(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// UnvoteReview handles DELETE /reviews/{id}/vote.
func (h *Trust) UnvoteReview(w http.ResponseWriter, r *http.Request) {
	req, ok := h.reviewRequest(w, r)
	if !ok {
		return
	}
	res, err := h.svc.UnvoteReview(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// CreateUpload handles POST /reviews/uploads — a slot to PUT a review photo into.
func (h *Trust) CreateUpload(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	var req trustapi.CreateUploadRequest
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

// ConfirmUpload handles POST /reviews/uploads/{id}/confirmation — the bytes are at the store.
func (h *Trust) ConfirmUpload(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	resourceID, err := pathID[id.Resource](r, "id")
	if failed(w, h.log, err) {
		return
	}
	req := trustapi.ConfirmUploadRequest{ActorID: uid, ID: resourceID}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.ConfirmUpload(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// ----------------------------------------------------------------- reports ---

// SubmitReport handles POST /reports.
func (h *Trust) SubmitReport(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	var req trustapi.SubmitReportRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID = uid
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.SubmitReport(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusCreated, res)
}

// ListMyReports handles GET /reports.
func (h *Trust) ListMyReports(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	limit, err := limitParam(r)
	if failed(w, h.log, err) {
		return
	}
	req := trustapi.ListReportsRequest{
		ActorID: uid,
		Status:  r.URL.Query().Get("status"),
		Cursor:  cursorParam(r),
		Limit:   limit,
	}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.ListMyReports(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteCursor(w, http.StatusOK, res.Data, cursorMeta(res.Meta.NextCursor))
}

// AdminListReports handles GET /admin/reports.
func (h *Trust) AdminListReports(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	limit, err := limitParam(r)
	if failed(w, h.log, err) {
		return
	}
	req := trustapi.AdminListReportsRequest{
		ActorID: uid,
		Status:  r.URL.Query().Get("status"),
		RefType: r.URL.Query().Get("ref_type"),
		Reason:  r.URL.Query().Get("reason"),
		Cursor:  cursorParam(r),
		Limit:   limit,
	}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.AdminListReports(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteCursor(w, http.StatusOK, res.Data, cursorMeta(res.Meta.NextCursor))
}

// AdminGetReport handles GET /admin/reports/{id}.
func (h *Trust) AdminGetReport(w http.ResponseWriter, r *http.Request) {
	req, ok := h.reportRequest(w, r)
	if !ok {
		return
	}
	res, err := h.svc.AdminGetReport(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// AdminClaimReport handles POST /admin/reports/{id}/claim.
func (h *Trust) AdminClaimReport(w http.ResponseWriter, r *http.Request) {
	req, ok := h.reportRequest(w, r)
	if !ok {
		return
	}
	res, err := h.svc.AdminClaimReport(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// AdminResolveReport handles POST /admin/reports/{id}/resolution.
func (h *Trust) AdminResolveReport(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	reportID, err := pathID[id.Report](r, "id")
	if failed(w, h.log, err) {
		return
	}
	var req trustapi.ResolveReportRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID, req.ID = uid, reportID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.AdminResolveReport(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// reviewRequest and reportRequest are the actor-plus-id shape several routes share.
func (h *Trust) reviewRequest(w http.ResponseWriter, r *http.Request) (trustapi.ReviewRequest, bool) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return trustapi.ReviewRequest{}, false
	}
	reviewID, err := pathID[id.Review](r, "id")
	if failed(w, h.log, err) {
		return trustapi.ReviewRequest{}, false
	}
	req := trustapi.ReviewRequest{ActorID: uid, ID: reviewID}
	if failed(w, h.log, check(h.v, req)) {
		return trustapi.ReviewRequest{}, false
	}
	return req, true
}

func (h *Trust) reportRequest(w http.ResponseWriter, r *http.Request) (trustapi.ReportRequest, bool) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return trustapi.ReportRequest{}, false
	}
	reportID, err := pathID[id.Report](r, "id")
	if failed(w, h.log, err) {
		return trustapi.ReportRequest{}, false
	}
	req := trustapi.ReportRequest{ActorID: uid, ID: reportID}
	if failed(w, h.log, check(h.v, req)) {
		return trustapi.ReportRequest{}, false
	}
	return req, true
}
