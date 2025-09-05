package inventoryecho

import (
	inventorybiz "shopnexus-remastered/internal/module/inventory/biz"

	"github.com/labstack/echo/v4"
)

type Handler struct {
	biz *inventorybiz.InventoryBiz
}

func NewHandler(e *echo.Echo, biz *inventorybiz.InventoryBiz) *Handler {
	h := &Handler{biz: biz}
	api := e.Group("/api/v1/inventory")
	_ = api

	return h
}
