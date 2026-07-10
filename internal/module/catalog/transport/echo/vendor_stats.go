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

// GetVendorStats returns aggregate statistics for a vendor account.
//
//	@Summary	Get vendor stats
//	@Tags		catalog
//	@Produce	json
//	@Param		account_id	query		string	true	"Vendor account ID (UUID)"
//	@Success	200			{object}	response.CommonResponse{data=catalogbiz.VendorStats}
//	@Failure	400			{object}	response.CommonResponse
//	@Router		/catalog/vendor-stats [get]
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
