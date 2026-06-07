package commonecho

import (
	commonbiz "shopnexus-server/internal/module/common/biz"

	"github.com/labstack/echo/v4"
)

// Handler handles HTTP requests for the common module.
type Handler struct {
	biz     commonbiz.CommonBizClient
	handler *commonbiz.CommonHandler // direct reference for non-Restate operations (file upload)
}

// NewHandler registers common module routes and returns the handler.
func NewHandler(e *echo.Echo, biz commonbiz.CommonBizClient, handler *commonbiz.CommonHandler) (*Handler, error) {
	h := &Handler{biz: biz, handler: handler}
	api := e.Group("/api/v1/common")

	api.POST("/files", h.UploadFile)
	api.GET("/option", h.ListOption)
	api.POST("/option", h.UpsertOptions)
	api.DELETE("/option", h.DeleteOptions)
	api.POST("/geocode/reverse", h.ReverseGeocode)
	api.POST("/geocode/forward", h.ForwardGeocode)
	api.GET("/geocode/search", h.SearchGeocode)
	api.GET("/stream", h.HandleSSE)
	api.GET("/currencies/rates", h.GetExchangeRates)

	return h, nil
}
