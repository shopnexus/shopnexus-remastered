package accountecho

import (
	"net/http"

	accountbiz "shopnexus-server/internal/module/account/biz"
	authclaims "shopnexus-server/internal/shared/claims"
	"shopnexus-server/internal/shared/response"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// Handler handles HTTP requests for the account module.
type Handler struct {
	biz accountbiz.AccountBizClient
}

// NewHandler registers account module routes and returns the handler.
func NewHandler(e *echo.Echo, biz accountbiz.AccountBizClient) *Handler {
	h := &Handler{biz: biz}
	api := e.Group("/api/v1/account")

	authApi := api.Group("/auth")
	authApi.POST("/login/basic", h.LoginBasic)
	authApi.POST("/register/basic", h.RegisterBasic)
	authApi.POST("/refresh", h.Refresh)

	// Account endpoints)
	api.GET("", h.GetAccount)
	// api.PATCH("", h.UpdateAccount)

	// Me endpoints
	meApi := api.Group("/me")
	meApi.GET("", h.GetMe)
	meApi.PATCH("", h.UpdateMe)

	// Profile endpoints
	profileApi := api.Group("/profile")
	profileApi.PATCH("/country", h.UpdateCountry)

	// Contact endpoints
	contactApi := api.Group("/contact")
	contactApi.GET("/:contact_id", h.GetContact)
	contactApi.GET("", h.ListContact)
	contactApi.POST("", h.CreateContact)
	contactApi.PATCH("", h.UpdateContact)
	contactApi.DELETE("", h.DeleteContact)

	// Favorite endpoints
	favoriteApi := api.Group("/favorite")
	favoriteApi.POST("/:spu_id", h.AddFavorite)
	favoriteApi.DELETE("/:spu_id", h.RemoveFavorite)
	favoriteApi.GET("", h.ListFavorite)

	// Notification endpoints
	notifApi := api.Group("/notification")
	notifApi.GET("", h.ListNotification)
	notifApi.GET("/unread-count", h.CountUnread)
	notifApi.POST("/read", h.MarkRead)
	notifApi.POST("/read-all", h.MarkAllRead)

	return h
}

type GetAccountRequest struct {
	AccountID uuid.UUID `query:"account_id" validate:"required"`
}

func (h *Handler) GetAccount(c echo.Context) error {
	var req GetAccountRequest
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

	profile, err := h.biz.GetProfile(c.Request().Context(), accountbiz.GetProfileParams{
		Issuer:    claims.Account,
		AccountID: req.AccountID,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, profile)
}
