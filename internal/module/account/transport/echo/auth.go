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

// LoginBasic authenticates with an identifier (username/email/phone) + password.
//
//	@Summary	Basic login
//	@Tags		account
//	@Accept		json
//	@Produce	json
//	@Param		body	body		LoginBasicRequest	true	"Credentials"
//	@Success	200		{object}	response.CommonResponse{data=AuthTokenResponse}
//	@Failure	400		{object}	response.CommonResponse
//	@Failure	401		{object}	response.CommonResponse
//	@Router		/account/auth/login/basic [post]
func (h *Handler) LoginBasic(c echo.Context) error {
	var req LoginBasicRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	result, err := h.biz.Call().Login(c.Request().Context(), accountbiz.LoginParams{
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

// RegisterBasic creates a new account from username/email/phone + password.
//
//	@Summary	Basic registration
//	@Tags		account
//	@Accept		json
//	@Produce	json
//	@Param		body	body		RegisterBasicRequest	true	"Registration payload"
//	@Success	201		{object}	response.CommonResponse{data=AuthTokenResponse}
//	@Failure	400		{object}	response.CommonResponse
//	@Router		/account/auth/register/basic [post]
func (h *Handler) RegisterBasic(c echo.Context) error {
	var req RegisterBasicRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	req.Country = strings.ToUpper(req.Country)
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	result, err := h.biz.Call().Register(c.Request().Context(), accountbiz.RegisterParams{
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

// Refresh exchanges a refresh token for a new access/refresh token pair.
//
//	@Summary	Refresh tokens
//	@Tags		account
//	@Accept		json
//	@Produce	json
//	@Param		body	body		RefreshRequest	true	"Refresh token"
//	@Success	200		{object}	response.CommonResponse{data=AuthTokenResponse}
//	@Failure	401		{object}	response.CommonResponse
//	@Router		/account/auth/refresh [post]
func (h *Handler) Refresh(c echo.Context) error {
	var req RefreshRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	result, err := h.biz.Call().Refresh(c.Request().Context(), req.RefreshToken)
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusUnauthorized, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, AuthTokenResponse{
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
	})
}
