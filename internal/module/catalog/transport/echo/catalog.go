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
	api.GET("/product-card", h.ListProductCard)

	api.GET("/product-spu", h.ListProductSpu)
	api.GET("/product-sku", h.ListProductSku)
	api.GET("/product-sku-attribute", h.ListProductSkuAttribute)

	return h
}

type ListProductCardRequest struct {
	sharedmodel.PaginationParams
}

func (h *Handler) ListProductCard(c echo.Context) error {
	var req ListProductCardRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	result, err := h.biz.ListProductCard(c.Request().Context(), catalogbiz.ListProductCardParams{
		PaginationParams: req.PaginationParams,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromPaginate(c.Response().Writer, result)
}

type ListProductSpuParams struct {
	sharedmodel.PaginationParams
	Code       []string `query:"code" comma_separated:"true" validate:"omitempty,dive,min=1,max=100"`
	VendorID   []int64  `query:"vendor_id" comma_separated:"true" validate:"omitempty,dive,gt=0"`
	CategoryID []int64  `query:"category_id" comma_separated:"true" validate:"omitempty,dive,gt=0"`
	BrandID    []int64  `query:"brand_id" comma_separated:"true" validate:"omitempty,dive,gt=0"`
	IsActive   []bool   `query:"is_active" comma_separated:"true" validate:"omitempty,dive"`
}

func (h *Handler) ListProductSpu(c echo.Context) error {
	var req ListProductSpuParams
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	result, err := h.biz.ListProductSpu(c.Request().Context(), catalogbiz.ListProductSpuParams{
		PaginationParams: req.PaginationParams,
		Code:             req.Code,
		AccountID:        req.VendorID,
		CategoryID:       req.CategoryID,
		BrandID:          req.BrandID,
		IsActive:         req.IsActive,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromPaginate(c.Response().Writer, result)
}

type ListProductSkuRequest struct {
	sharedmodel.PaginationParams
	Code  []string `query:"code" comma_separated:"true" validate:"omitempty,dive,min=1,max=100"`
	SpuID []int64  `query:"spu_id" comma_separated:"true" validate:"omitempty,dive,gt=0"`
	Price []int64  `query:"price" comma_separated:"true" validate:"omitempty,dive,gt=0"`
}

func (h *Handler) ListProductSku(c echo.Context) error {
	var req ListProductSkuRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	result, err := h.biz.ListProductSku(c.Request().Context(), catalogbiz.ListProductSkuParams{
		PaginationParams: req.PaginationParams,
		Code:             req.Code,
		SpuID:            req.SpuID,
		Price:            req.Price,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromPaginate(c.Response().Writer, result)
}

type ListProductSkuAttributeRequest struct {
	sharedmodel.PaginationParams
	Name []string `query:"name" comma_separated:"true" validate:"omitempty,dive,min=1,max=100"`
}

func (h *Handler) ListProductSkuAttribute(c echo.Context) error {
	var req ListProductSkuAttributeRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	result, err := h.biz.ListProductSkuAttribute(c.Request().Context(), catalogbiz.ListProductSkuAttributeParams{
		PaginationParams: req.PaginationParams,
		Name:             req.Name,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromPaginate(c.Response().Writer, result)
}
