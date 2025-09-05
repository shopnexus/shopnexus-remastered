package accountecho

import (
	"fmt"
	"net/http"
	"shopnexus-remastered/internal/db"
	accountbiz "shopnexus-remastered/internal/module/account/biz"
	authbiz "shopnexus-remastered/internal/module/auth/biz"
	"shopnexus-remastered/internal/module/shared/transport/echo/response"
	"shopnexus-remastered/internal/utils/pgutil"

	"github.com/labstack/echo/v4"
)

type Handler struct {
	biz *accountbiz.AccountBiz
}

func NewHandler(e *echo.Echo, biz *accountbiz.AccountBiz) *Handler {
	h := &Handler{biz: biz}
	api := e.Group("/api/v1/account")
	api.GET("/", h.GetAccount)
	api.GET("/me", h.GetMe)

	return h
}

type GetAccountRequest struct {
	Code     *string `query:"code" validate:"omitempty,uuid4"`
	Username *string `query:"username" validate:"omitempty,min=1,max=255"`
	Email    *string `query:"email" validate:"omitempty,email"`
	Phone    *string `query:"phone" validate:"omitempty,e164"`
}

type GetAccountResponse struct {
	Code        string           `json:"code"`
	Type        db.AccountType   `json:"type"`
	Status      db.AccountStatus `json:"status"`
	Phone       *string          `json:"phone"`
	Email       *string          `json:"email"`
	Username    *string          `json:"username"`
	DateCreated int64            `json:"date_created"`
	DateUpdated int64            `json:"date_updated"`
}

func (h *Handler) GetAccount(c echo.Context) error {
	var req GetAccountRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	fmt.Println(req)

	result, err := h.biz.Find(c.Request().Context(), accountbiz.FindParams{
		Code:     req.Code,
		Username: req.Username,
		Email:    req.Email,
		Phone:    req.Phone,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, GetAccountResponse{
		Code:        result.Code,
		Type:        result.Type,
		Status:      result.Status,
		Phone:       pgutil.PgtypeToPtr[string](result.Phone),
		Email:       pgutil.PgtypeToPtr[string](result.Email),
		Username:    pgutil.PgtypeToPtr[string](result.Username),
		DateCreated: result.DateCreated.Time.UnixMilli(),
		DateUpdated: result.DateUpdated.Time.UnixMilli(),
	})
}

func (h *Handler) GetMe(c echo.Context) error {
	claims, err := authbiz.GetClaims(c.Request())
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusUnauthorized, err)
	}

	result, err := h.biz.Find(c.Request().Context(), accountbiz.FindParams{
		Code: &claims.Code,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, GetAccountResponse{
		Code:        result.Code,
		Type:        result.Type,
		Status:      result.Status,
		Phone:       pgutil.PgtypeToPtr[string](result.Phone),
		Email:       pgutil.PgtypeToPtr[string](result.Email),
		Username:    pgutil.PgtypeToPtr[string](result.Username),
		DateCreated: result.DateCreated.Time.UnixMilli(),
		DateUpdated: result.DateUpdated.Time.UnixMilli(),
	})
}
