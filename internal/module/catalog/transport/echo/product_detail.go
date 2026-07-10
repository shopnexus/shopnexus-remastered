package catalogecho

import (
	"net/http"

	catalogbiz "shopnexus-server/internal/module/catalog/biz"
	authclaims "shopnexus-server/internal/shared/claims"
	"shopnexus-server/internal/shared/response"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/labstack/echo/v4"
)

type GetProductDetailRequest struct {
	ID   uuid.NullUUID `query:"id"   validate:"omitnil"`
	Slug null.String   `query:"slug" validate:"omitnil"`
}

// GetProductDetail returns the full product detail by ID or slug.
//
//	@Summary	Get product detail
//	@Tags		catalog
//	@Produce	json
//	@Param		id		query		string	false	"Product SPU ID (UUID)"
//	@Param		slug	query		string	false	"Product slug"
//	@Success	200		{object}	response.CommonResponse{data=catalogmodel.ProductDetail}
//	@Failure	400		{object}	response.CommonResponse
//	@Router		/catalog/product-detail [get]
func (h *Handler) GetProductDetail(c echo.Context) error {
	var req GetProductDetailRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	params := catalogbiz.GetProductDetailParams{
		ID:   req.ID,
		Slug: req.Slug,
	}

	// Optionally pass authenticated account for view tracking
	if claims, err := authclaims.GetClaims(c.Request()); err == nil {
		params.Account = &claims.Account
	}

	result, err := h.biz.GetProductDetail(c.Request().Context(), params)
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}
