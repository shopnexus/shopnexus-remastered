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

// ListProductCard returns a paginated list of product cards with filters.
//
//	@Summary	List product cards
//	@Tags		catalog
//	@Produce	json
//	@Param		page				query		int			false	"Page number (offset mode)"	minimum(1)
//	@Param		limit				query		int			false	"Items per page (max 100)"	minimum(1)	maximum(100)
//	@Param		cursor				query		string		false	"Keyset cursor (cursor mode)"
//	@Param		sort				query		string		false	"Sort, e.g. -date_created"
//	@Param		seller_id			query		string		false	"Filter by seller ID (UUID)"
//	@Param		category_id			query		[]string	false	"Filter by category IDs (UUID)"
//	@Param		tag					query		[]string	false	"Filter by tags"
//	@Param		search				query		string		false	"Search term"
//	@Param		price_min			query		number		false	"Minimum price"
//	@Param		price_max			query		number		false	"Maximum price"
//	@Param		date_created_from	query		int			false	"Created after (unix)"
//	@Param		date_created_to		query		int			false	"Created before (unix)"
//	@Success	200					{object}	response.SwaggerPaginationResponse{data=[]catalogmodel.ProductCard}
//	@Failure	400					{object}	response.CommonResponse
//	@Router		/catalog/product-card [get]
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

	result, err := h.biz.ListProductCard(c.Request().Context(), params)
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromPaginate(c.Response().Writer, result)
}

// GetProductCard returns a single product card by SPU ID.
//
//	@Summary	Get product card
//	@Tags		catalog
//	@Produce	json
//	@Param		id	path		string	true	"Product SPU ID (UUID)"
//	@Success	200	{object}	response.CommonResponse{data=catalogmodel.ProductCard}
//	@Failure	400	{object}	response.CommonResponse
//	@Router		/catalog/product-card/{id} [get]
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

	result, err := h.biz.GetProductCard(c.Request().Context(), params)
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}

type ListRecommendedProductCardParams struct {
	Limit int `query:"limit" validate:"omitempty"`
}

// ListRecommendedProductCard returns recommended product cards for the caller.
//
//	@Summary	List recommended product cards
//	@Tags		catalog
//	@Produce	json
//	@Param		limit	query		int	false	"Max items to return"
//	@Success	200		{object}	response.CommonResponse{data=[]catalogmodel.ProductCard}
//	@Failure	400		{object}	response.CommonResponse
//	@Router		/catalog/product-card/recommended [get]
func (h *Handler) ListRecommendedProductCard(c echo.Context) error {
	var req ListRecommendedProductCardParams
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	claims, _ := authclaims.GetClaims(c.Request())

	result, err := h.biz.ListRecommendedProductCard(c.Request().Context(), catalogbiz.ListRecommendedProductCardParams{
		Account: claims.Account,
		Limit:   req.Limit,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}
