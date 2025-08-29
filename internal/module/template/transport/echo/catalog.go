package catalogecho

import (
	catalogbiz "shopnexus-remastered/internal/module/catalog/biz"

	"github.com/labstack/echo/v4"
)

type Handler struct {
	biz *catalogbiz.CatalogBiz
}

func NewHandler(e *echo.Echo, catalogbiz *catalogbiz.CatalogBiz) *Handler {
	h := &Handler{biz: catalogbiz}
	api := e.Group("/api/v1/catalog")
	_ = api

	return h
}
