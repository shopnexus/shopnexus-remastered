package catalogecho

import (
	"net/http"

	catalogbiz "shopnexus-server/internal/module/catalog/biz"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/response"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/labstack/echo/v4"
)

type ListCategoryRequest struct {
	paginate.Params

	ID     []uuid.UUID `query:"id"     validate:"omitempty,dive,gt=0"`
	Search null.String `query:"search" validate:"omitnil"`
}

// ListCategory returns a paginated list of product categories.
//
//	@Summary	List categories
//	@Tags		catalog
//	@Produce	json
//	@Param		page	query		int			false	"Page number (offset mode)"	minimum(1)
//	@Param		limit	query		int			false	"Items per page (max 100)"	minimum(1)	maximum(100)
//	@Param		cursor	query		string		false	"Keyset cursor (cursor mode)"
//	@Param		sort	query		string		false	"Sort, e.g. -date_created"
//	@Param		id		query		[]string	false	"Filter by category IDs (UUID)"
//	@Param		search	query		string		false	"Search term"
//	@Success	200		{object}	response.SwaggerPaginationResponse{data=[]catalogmodel.Category}
//	@Failure	400		{object}	response.CommonResponse
//	@Router		/catalog/category [get]
func (h *Handler) ListCategory(c echo.Context) error {
	var req ListCategoryRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	result, err := h.biz.ListCategory(c.Request().Context(), catalogbiz.ListCategoryParams{
		Params: req.Params.Constrain(),
		ID:     req.ID,
		Search: req.Search,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromPaginate(c.Response().Writer, result)
}

type GetCategoryRequest struct {
	ID uuid.UUID `param:"id" validate:"required"`
}

// GetCategory returns a single category by ID.
//
//	@Summary	Get category
//	@Tags		catalog
//	@Produce	json
//	@Param		id	path		string	true	"Category ID (UUID)"
//	@Success	200	{object}	response.CommonResponse{data=catalogmodel.Category}
//	@Failure	400	{object}	response.CommonResponse
//	@Router		/catalog/category/{id} [get]
func (h *Handler) GetCategory(c echo.Context) error {
	var req GetCategoryRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	result, err := h.biz.ListCategory(c.Request().Context(), catalogbiz.ListCategoryParams{
		Params: paginate.Params{
			Limit: null.Int32From(1),
		}.Constrain(),
		ID: []uuid.UUID{req.ID},
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, result.Data[0])
}
