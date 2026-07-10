package catalogecho

import (
	"net/http"

	catalogbiz "shopnexus-server/internal/module/catalog/biz"
	authclaims "shopnexus-server/internal/shared/claims"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/response"

	"github.com/guregu/null/v6"
	"github.com/labstack/echo/v4"
)

type ListTagRequest struct {
	paginate.Params

	Search null.String `query:"search" validate:"omitnil,max=100"`
}

// ListTag returns a paginated list of product tags.
//
//	@Summary	List tags
//	@Tags		catalog
//	@Produce	json
//	@Param		page	query		int		false	"Page number (offset mode)"	minimum(1)
//	@Param		limit	query		int		false	"Items per page (max 100)"	minimum(1)	maximum(100)
//	@Param		cursor	query		string	false	"Keyset cursor (cursor mode)"
//	@Param		sort	query		string	false	"Sort, e.g. -date_created"
//	@Param		search	query		string	false	"Search term"
//	@Success	200		{object}	response.SwaggerPaginationResponse{data=[]catalogdb.CatalogTag}
//	@Failure	400		{object}	response.CommonResponse
//	@Router		/catalog/tag [get]
func (h *Handler) ListTag(c echo.Context) error {
	var req ListTagRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	result, err := h.biz.ListTag(c.Request().Context(), catalogbiz.ListTagParams{
		Params: req.Params.Constrain(),
		Search: req.Search,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromPaginate(c.Response().Writer, result)
}

type GetTagRequest struct {
	Tag string `param:"tag" validate:"required,min=1,max=100"`
}

// GetTag returns a single tag by name.
//
//	@Summary	Get tag
//	@Tags		catalog
//	@Produce	json
//	@Security	BearerAuth
//	@Param		tag	path		string	true	"Tag name"
//	@Success	200	{object}	response.CommonResponse{data=catalogdb.CatalogTag}
//	@Failure	400	{object}	response.CommonResponse
//	@Failure	401	{object}	response.CommonResponse
//	@Router		/catalog/tag/{tag} [get]
func (h *Handler) GetTag(c echo.Context) error {
	var req GetTagRequest
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

	result, err := h.biz.GetTag(c.Request().Context(), catalogbiz.GetTagParams{
		Account: claims.Account,
		Tag:     req.Tag,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}
