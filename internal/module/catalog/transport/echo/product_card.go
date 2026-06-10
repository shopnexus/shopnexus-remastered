package catalogecho

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/labstack/echo/v4"

	catalogbiz "shopnexus-server/internal/module/catalog/biz"
	authclaims "shopnexus-server/internal/shared/claims"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/response"
)

type ListProductCardRequest struct {
	paginate.Params

	SellerID        uuid.NullUUID `query:"seller_id"         validate:"omitnil"`
	CategoryID      []uuid.UUID   `query:"category_id"       validate:"omitempty"     comma_separated:"true"`
	Tags            []string      `query:"tag"               validate:"omitempty"`
	Search          null.String   `query:"search"            validate:"omitnil"`
	PriceMin        null.Float    `query:"price_min"         validate:"omitnil,gte=0"`
	PriceMax        null.Float    `query:"price_max"         validate:"omitnil,gte=0"`
	DateCreatedFrom null.Int      `query:"date_created_from" validate:"omitnil,gte=0"`
	DateCreatedTo   null.Int      `query:"date_created_to"   validate:"omitnil,gte=0"`
}

func (h *Handler) ListProductCard(c echo.Context) error {
	var req ListProductCardRequest

	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	params := catalogbiz.ListProductCardParams{
		Params:          req.Params.Constrain(),
		SellerID:        req.SellerID,
		CategoryID:      req.CategoryID,
		Tags:            req.Tags,
		Search:          req.Search,
		PriceMin:        req.PriceMin,
		PriceMax:        req.PriceMax,
		DateCreatedFrom: req.DateCreatedFrom,
		DateCreatedTo:   req.DateCreatedTo,
	}

	if claims, err := authclaims.GetClaims(c.Request()); err == nil {
		params.AccountID = uuid.NullUUID{UUID: claims.Account.ID, Valid: true}
	}

	result, err := h.biz.Guaranteed().ListProductCard(c.Request().Context(), params)
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromPaginate(c.Response().Writer, result)
}

func (h *Handler) GetProductCard(c echo.Context) error {
	spuID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	params := catalogbiz.GetProductCardParams{
		SpuID: spuID,
	}

	if claims, err := authclaims.GetClaims(c.Request()); err == nil {
		params.AccountID = uuid.NullUUID{UUID: claims.Account.ID, Valid: true}
	}

	result, err := h.biz.Guaranteed().GetProductCard(c.Request().Context(), params)
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}

type ListRecommendedProductCardParams struct {
	Limit int `query:"limit" validate:"omitempty"`
}

func (h *Handler) ListRecommendedProductCard(c echo.Context) error {
	var req ListRecommendedProductCardParams
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	claims, _ := authclaims.GetClaims(c.Request())

	result, err := h.biz.Guaranteed().ListRecommendedProductCard(c.Request().Context(), catalogbiz.ListRecommendedProductCardParams{
		Account: claims.Account,
		Limit:   req.Limit,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}
