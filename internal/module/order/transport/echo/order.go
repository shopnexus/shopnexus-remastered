package orderecho

import (
	orderbiz "shopnexus-remastered/internal/module/order/biz"

	"github.com/labstack/echo/v4"
)

type Handler struct {
	biz *orderbiz.OrderBiz
}

func NewHandler(e *echo.Echo, biz *orderbiz.OrderBiz) *Handler {
	h := &Handler{biz: biz}
	api := e.Group("/api/v1/order")
	_ = api

	return h
}
