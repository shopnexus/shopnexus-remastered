package echo

import (
	"net/http"

	"shopnexus-remastered/internal/db"
	"shopnexus-remastered/internal/module/auth/biz"
	"shopnexus-remastered/internal/module/shared/transport/echo/response"

	"github.com/labstack/echo/v4"
)

type Handler struct {
	biz *authbiz.AuthBiz
}

func NewHandler(e *echo.Echo, authbiz *authbiz.AuthBiz) *Handler {
	h := &Handler{biz: authbiz}
	api := e.Group("/api/v1/auth")
	api.POST("/login/basic", h.LoginBasic)
	api.POST("/register/basic", h.RegisterBasic)

	return h
}

type LoginBasicRequest struct {
	Username *string `json:"username" validate:"omitempty,min=1,max=255"`
	Email    *string `json:"email" validate:"omitempty,email"`
	Phone    *string `json:"phone" validate:"omitempty,e164"`
	Password string  `json:"password" validate:"required,min=8,max=72"`
}

type LoginBasicResponse struct {
	AccessToken string `json:"access_token"`
}

func (h *Handler) LoginBasic(c echo.Context) error {
	var req LoginBasicRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	result, err := h.biz.Login(c.Request().Context(), authbiz.LoginParams{
		Username: req.Username,
		Email:    req.Email,
		Phone:    req.Phone,
		Password: &req.Password,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusUnauthorized, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, LoginBasicResponse{
		AccessToken: result.AccessToken,
	})
}

type RegisterBasicRequest struct {
	Type     db.AccountType `json:"type" validate:"required,oneof=Customer Vendor"`
	Username *string        `json:"username" validate:"omitempty,min=1,max=255"`
	Email    *string        `json:"email" validate:"omitempty,email"`
	Phone    *string        `json:"phone" validate:"omitempty,e164"`
	Password *string        `json:"password" validate:"omitempty,min=8,max=72"`
}

type RegisterBasicResponse struct {
	AccessToken string `json:"access_token"`
}

func (h *Handler) RegisterBasic(c echo.Context) error {
	var req RegisterBasicRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	result, err := h.biz.Register(c.Request().Context(), authbiz.RegisterParams{
		Type:     req.Type,
		Username: req.Username,
		Email:    req.Email,
		Phone:    req.Phone,
		Password: req.Password,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusCreated, RegisterBasicResponse{
		AccessToken: result.AccessToken,
	})
}
