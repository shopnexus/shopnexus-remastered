package accountecho

import (
	"net/http"
	"strings"

	accountbiz "shopnexus-server/internal/module/account/biz"
	authclaims "shopnexus-server/internal/shared/claims"
	sharedcurrency "shopnexus-server/internal/shared/currency"
	"shopnexus-server/internal/shared/response"

	"github.com/labstack/echo/v4"
)

type updateCountryRequest struct {
	Country string `json:"country" validate:"required,len=2,uppercase,alpha"`
}

// UpdateCountry handles PATCH /account/profile/country.
// Returns 409 when the caller's wallet balance is non-zero; the terminal
// conflict error raised by the biz layer is translated to the HTTP status by
// response.FromError via restate.IsTerminalError.
func (h *Handler) UpdateCountry(c echo.Context) error {
	var req updateCountryRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	req.Country = strings.ToUpper(req.Country)
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	claims, err := authclaims.GetClaims(c.Request())
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusUnauthorized, err)
	}

	if err := h.biz.Call().UpdateCountry(c.Request().Context(), accountbiz.UpdateCountryParams{
		AccountID: claims.Account.ID,
		Country:   req.Country,
	}); err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	inferred, _ := sharedcurrency.Infer(req.Country)
	return response.FromDTO(c.Response().Writer, http.StatusOK, echo.Map{
		"country":           req.Country,
		"inferred_currency": inferred,
	})
}
