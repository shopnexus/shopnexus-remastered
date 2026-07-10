package orderecho

import (
	"net/http"

	orderbiz "shopnexus-server/internal/module/order/biz"
	ordermodel "shopnexus-server/internal/module/order/model"
	authclaims "shopnexus-server/internal/shared/claims"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/response"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// --- List + Get disputes ---

type ListRefundDisputesRequest struct {
	paginate.Params

	Status string `query:"status" validate:"omitempty,oneof=Open SellerWins BuyerWins"`
}

// ListRefundDisputes returns refund disputes visible to the caller (paginated).
//
//	@Summary	List refund disputes
//	@Tags		order
//	@Produce	json
//	@Security	BearerAuth
//	@Param		status	query		string	false	"Dispute status"			Enums(Open, SellerWins, BuyerWins)
//	@Param		page	query		int		false	"Page number (offset mode)"	minimum(1)
//	@Param		limit	query		int		false	"Items per page (max 100)"	minimum(1)	maximum(100)
//	@Param		cursor	query		string	false	"Keyset cursor (cursor mode)"
//	@Param		sort	query		string	false	"Sort, e.g. -date_created"
//	@Success	200		{object}	response.SwaggerPaginationResponse{data=[]ordermodel.RefundDispute}
//	@Failure	401		{object}	response.CommonResponse
//	@Router		/order/disputes [get]
func (h *Handler) ListRefundDisputes(c echo.Context) error {
	var req ListRefundDisputesRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	claims, err := authclaims.GetClaims(c.Request())
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusUnauthorized, err)
	}
	result, err := h.biz.ListRefundDisputes(c.Request().Context(), orderbiz.ListRefundDisputesParams{
		Account: claims.Account,
		Status:  ordermodel.DisputeStatus(req.Status),
		Params:  req.Params.Constrain(),
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromPaginate(c.Response().Writer, result)
}

// ListRefundDisputesByRefund returns disputes for a specific refund (paginated).
//
//	@Summary	List refund disputes by refund
//	@Tags		order
//	@Produce	json
//	@Security	BearerAuth
//	@Param		refundID	path		string	true	"Refund ID (UUID)"
//	@Param		status		query		string	false	"Dispute status"			Enums(Open, SellerWins, BuyerWins)
//	@Param		page		query		int		false	"Page number (offset mode)"	minimum(1)
//	@Param		limit		query		int		false	"Items per page (max 100)"	minimum(1)	maximum(100)
//	@Param		cursor		query		string	false	"Keyset cursor (cursor mode)"
//	@Param		sort		query		string	false	"Sort, e.g. -date_created"
//	@Success	200			{object}	response.SwaggerPaginationResponse{data=[]ordermodel.RefundDispute}
//	@Failure	400			{object}	response.CommonResponse
//	@Failure	401			{object}	response.CommonResponse
//	@Router		/order/refunds/{refundID}/disputes [get]
func (h *Handler) ListRefundDisputesByRefund(c echo.Context) error {
	refundID, err := uuid.Parse(c.Param("refundID"))
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	var req ListRefundDisputesRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	claims, err := authclaims.GetClaims(c.Request())
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusUnauthorized, err)
	}
	result, err := h.biz.ListRefundDisputes(c.Request().Context(), orderbiz.ListRefundDisputesParams{
		Account:  claims.Account,
		RefundID: uuid.NullUUID{UUID: refundID, Valid: true},
		Status:   ordermodel.DisputeStatus(req.Status),
		Params:   req.Params.Constrain(),
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromPaginate(c.Response().Writer, result)
}

// GetRefundDispute returns a single refund dispute by ID.
//
//	@Summary	Get refund dispute
//	@Tags		order
//	@Produce	json
//	@Security	BearerAuth
//	@Param		disputeID	path		string	true	"Dispute ID (UUID)"
//	@Success	200			{object}	response.CommonResponse{data=ordermodel.RefundDispute}
//	@Failure	400			{object}	response.CommonResponse
//	@Failure	401			{object}	response.CommonResponse
//	@Router		/order/disputes/{disputeID} [get]
func (h *Handler) GetRefundDispute(c echo.Context) error {
	disputeID, err := uuid.Parse(c.Param("disputeID"))
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	claims, err := authclaims.GetClaims(c.Request())
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusUnauthorized, err)
	}
	result, err := h.biz.GetRefundDispute(c.Request().Context(), orderbiz.GetRefundDisputeParams{
		Account:   claims.Account,
		DisputeID: disputeID,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}

// --- Admin: uphold (seller wins) / dismiss (buyer wins) ---

type AdminDisputeDecisionRequest struct {
	Note string `json:"resolution_note" validate:"required,min=1,max=2000"`
}

// AdminUpholdDispute resolves a dispute in the seller's favour (admin only).
//
//	@Summary	Admin uphold dispute
//	@Tags		order
//	@Accept		json
//	@Produce	json
//	@Security	BearerAuth
//	@Param		disputeID	path		string						true	"Dispute ID (UUID)"
//	@Param		body		body		AdminDisputeDecisionRequest	true	"Resolution payload"
//	@Success	200			{object}	response.CommonResponse{data=ordermodel.RefundDispute}
//	@Failure	400			{object}	response.CommonResponse
//	@Failure	401			{object}	response.CommonResponse
//	@Router		/order/disputes/{disputeID}/uphold [post]
func (h *Handler) AdminUpholdDispute(c echo.Context) error {
	return h.adminDecideDispute(c, true)
}

// AdminDismissDispute resolves a dispute in the buyer's favour (admin only).
//
//	@Summary	Admin dismiss dispute
//	@Tags		order
//	@Accept		json
//	@Produce	json
//	@Security	BearerAuth
//	@Param		disputeID	path		string						true	"Dispute ID (UUID)"
//	@Param		body		body		AdminDisputeDecisionRequest	true	"Resolution payload"
//	@Success	200			{object}	response.CommonResponse{data=ordermodel.RefundDispute}
//	@Failure	400			{object}	response.CommonResponse
//	@Failure	401			{object}	response.CommonResponse
//	@Router		/order/disputes/{disputeID}/dismiss [post]
func (h *Handler) AdminDismissDispute(c echo.Context) error {
	return h.adminDecideDispute(c, false)
}

func (h *Handler) adminDecideDispute(c echo.Context, uphold bool) error {
	disputeID, err := uuid.Parse(c.Param("disputeID"))
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	var req AdminDisputeDecisionRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	claims, err := authclaims.GetClaims(c.Request())
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusUnauthorized, err)
	}
	params := orderbiz.AdminDisputeDecisionParams{
		Account:   claims.Account,
		DisputeID: disputeID,
		Note:      req.Note,
	}
	var result interface{}
	if uphold {
		result, err = h.biz.Call().AdminUpholdDispute(c.Request().Context(), params)
	} else {
		result, err = h.biz.Call().AdminDismissDispute(c.Request().Context(), params)
	}
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}
