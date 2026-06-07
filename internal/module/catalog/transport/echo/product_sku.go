package catalogecho

import (
	"encoding/json"
	"net/http"

	catalogbiz "shopnexus-server/internal/module/catalog/biz"
	catalogmodel "shopnexus-server/internal/module/catalog/model"
	authclaims "shopnexus-server/internal/shared/claims"
	"shopnexus-server/internal/shared/response"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/labstack/echo/v4"
)

type ListProductSkuRequest struct {
	SpuID           uuid.UUID  `query:"spu_id"     validate:"omitempty"`
	PriceFrom       null.Int64 `query:"price_from" validate:"omitnil,gt=0"`
	PriceTo         null.Int64 `query:"price_to"   validate:"omitnil,gt=0,gtefield=PriceFrom"`
	SharedPackaging null.Bool  `query:"combinable" validate:"omitnil"`
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
		SpuID:           []uuid.UUID{req.SpuID},
		PriceFrom:       req.PriceFrom,
		PriceTo:         req.PriceTo,
		SharedPackaging: req.SharedPackaging,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}

type CreateProductSkuRequest struct {
	SpuID           uuid.UUID                       `json:"spu_id"          validate:"required"`
	Price           int64                           `json:"price"           validate:"required,gt=0"`
	SharedPackaging bool                            `json:"combinable"      validate:"omitempty"`
	Attributes      []catalogmodel.ProductAttribute `json:"attributes"      validate:"omitempty,dive"`
	PackageDetails  json.RawMessage                 `json:"package_details" validate:"required"`
}

func (h *Handler) CreateProductSku(c echo.Context) error {
	var req CreateProductSkuRequest
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

	result, err := h.biz.CreateProductSku(c.Request().Context(), catalogbiz.CreateProductSkuParams{
		Account:         claims.Account,
		SpuID:           req.SpuID,
		Price:           req.Price,
		SharedPackaging: req.SharedPackaging,
		Attributes:      req.Attributes,
		PackageDetails:  req.PackageDetails,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}

type UpdateProductSkuRequest struct {
	ID              uuid.UUID                       `json:"id"              validate:"required"`
	Price           null.Int                        `json:"price"           validate:"omitnil,gt=0"`
	SharedPackaging null.Bool                       `json:"combinable"      validate:"omitnil"`
	Attributes      []catalogmodel.ProductAttribute `json:"attributes"      validate:"omitempty,dive"`
	PackageDetails  json.RawMessage                 `json:"package_details" validate:"omitempty"`
}

func (h *Handler) UpdateProductSku(c echo.Context) error {
	var req UpdateProductSkuRequest
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

	result, err := h.biz.UpdateProductSku(c.Request().Context(), catalogbiz.UpdateProductSkuParams{
		Account:         claims.Account,
		ID:              req.ID,
		Price:           req.Price,
		SharedPackaging: req.SharedPackaging,
		Attributes:      req.Attributes,
		PackageDetails:  req.PackageDetails,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}

type DeleteProductSkuRequest struct {
	ID uuid.UUID `json:"id" validate:"required"`
}

func (h *Handler) DeleteProductSku(c echo.Context) error {
	var req DeleteProductSkuRequest
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

	if err := h.biz.DeleteProductSku(c.Request().Context(), catalogbiz.DeleteProductSkuParams{
		Account: claims.Account,
		ID:      req.ID,
	}); err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromMessage(c.Response().Writer, http.StatusOK, "Delete product sku successfully")
}
