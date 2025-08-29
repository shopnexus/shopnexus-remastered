package catalogecho

import (
	"net/http"

	catalogbiz "shopnexus-remastered/internal/module/catalog/biz"
	sharedmodel "shopnexus-remastered/internal/module/shared/model"
	"shopnexus-remastered/internal/module/shared/transport/echo/response"

	"github.com/labstack/echo/v4"
)

type Handler struct {
	biz *catalogbiz.CatalogBiz
}

func NewHandler(e *echo.Echo, catalogbiz *catalogbiz.CatalogBiz) *Handler {
	h := &Handler{biz: catalogbiz}
	api := e.Group("/api/v1/catalog")
	api.GET("/", h.ListProduct)

	return h
}

type ListProductRequest struct {
	sharedmodel.PaginationParams
}

func (h *Handler) ListProduct(c echo.Context) error {
	var req ListProductRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	result, err := h.biz.ListProduct(c.Request().Context(), catalogbiz.ListProductParams{
		PaginationParams: req.PaginationParams,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromPaginate(c.Response().Writer, result)
}
