package catalogecho

import (
	"net/http"

	catalogbiz "shopnexus-server/internal/module/catalog/biz"
	"shopnexus-server/internal/shared/response"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

type GetVendorStatsRequest struct {
	AccountID uuid.UUID `query:"account_id" validate:"required"`
}

func (h *Handler) GetVendorStats(c echo.Context) error {
	var req GetVendorStatsRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	result, err := h.biz.GetVendorStats(c.Request().Context(), catalogbiz.GetVendorStatsParams{
		AccountID: req.AccountID,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}
