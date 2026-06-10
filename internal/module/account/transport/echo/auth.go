package accountecho

import (
	"net/http"
	"strings"

	accountbiz "shopnexus-server/internal/module/account/biz"
	"shopnexus-server/internal/shared/response"

	"github.com/guregu/null/v6"
	"github.com/labstack/echo/v4"
)

type LoginBasicRequest struct {
	ID       string `json:"id"       validate:"required"`
	Password string `json:"password" validate:"required"`
}

type AuthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

func (h *Handler) LoginBasic(c echo.Context) error {
	var req LoginBasicRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	result, err := h.biz.Guaranteed().Login(c.Request().Context(), accountbiz.LoginParams{
		Username: null.NewString(req.ID, true),
		Email:    null.NewString(req.ID, true),
		Phone:    null.NewString(req.ID, true),
		Password: null.NewString(req.Password, true),
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusUnauthorized, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, AuthTokenResponse{
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
	})
}

type RegisterBasicRequest struct {
	Username null.String `json:"username" validate:"omitnil"`
	Email    null.String `json:"email"    validate:"omitnil"`
	Phone    null.String `json:"phone"    validate:"omitnil"`
	Password string      `json:"password" validate:"required"`
	Country  string      `json:"country"  validate:"required,len=2,uppercase,alpha"`
}

func (h *Handler) RegisterBasic(c echo.Context) error {
	var req RegisterBasicRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	req.Country = strings.ToUpper(req.Country)
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	result, err := h.biz.Guaranteed().Register(c.Request().Context(), accountbiz.RegisterParams{
		Username: req.Username,
		Email:    req.Email,
		Phone:    req.Phone,
		Password: null.NewString(req.Password, true),
		Country:  req.Country,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusCreated, AuthTokenResponse{
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
	})
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" validate:"required"`
}

func (h *Handler) Refresh(c echo.Context) error {
	var req RefreshRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	result, err := h.biz.Guaranteed().Refresh(c.Request().Context(), req.RefreshToken)
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusUnauthorized, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, AuthTokenResponse{
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
	})
}
