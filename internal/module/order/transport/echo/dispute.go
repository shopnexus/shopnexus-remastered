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
	Status string `query:"status" validate:"omitempty,oneof=Open SellerWins BuyerWins"`
	paginate.Params
}

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

func (h *Handler) AdminUpholdDispute(c echo.Context) error {
	return h.adminDecideDispute(c, true)
}

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
		result, err = h.biz.AdminUpholdDispute(c.Request().Context(), params)
	} else {
		result, err = h.biz.AdminDismissDispute(c.Request().Context(), params)
	}
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}
