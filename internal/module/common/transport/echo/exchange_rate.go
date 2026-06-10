package commonecho

import (
	"net/http"

	"github.com/labstack/echo/v4"

	commonbiz "shopnexus-server/internal/module/common/biz"
	"shopnexus-server/internal/shared/response"
)

// GetExchangeRates returns the latest exchange rate snapshot.
// Public endpoint; no auth required.
func (h *Handler) GetExchangeRates(c echo.Context) error {
	snap, err := h.biz.GetExchangeRates(c.Request().Context(), commonbiz.GetExchangeRatesParams{})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromDTO(c.Response().Writer, http.StatusOK, snap)
}
