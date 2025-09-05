package accountecho

import (
	"net/http"
	accountbiz "shopnexus-remastered/internal/module/account/biz"
	"shopnexus-remastered/internal/module/shared/transport/echo/response"

	"github.com/labstack/echo/v4"
)

type GetCart struct {
}

type GetCartResponse struct {
}

func (h *Handler) GetCart(c echo.Context) error {
	var req GetAccountRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	result, err := h.biz.GetCart(c.Request().Context(), accountbiz.GetCartParams{
		AccountID: 1,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}
