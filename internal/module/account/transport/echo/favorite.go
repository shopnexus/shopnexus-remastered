package accountecho

import (
	"net/http"

	accountbiz "shopnexus-server/internal/module/account/biz"
	authclaims "shopnexus-server/internal/shared/claims"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/response"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

type AddFavoriteRequest struct {
	SpuID uuid.UUID `param:"spu_id" validate:"required"`
}

// AddFavorite marks an SPU as favorited by the caller.
//
//	@Summary	Add favorite
//	@Tags		account
//	@Produce	json
//	@Security	BearerAuth
//	@Param		spu_id	path		string	true	"SPU ID (UUID)"
//	@Success	200		{object}	response.CommonResponse{data=accountdb.AccountFavorite}
//	@Failure	401		{object}	response.CommonResponse
//	@Router		/account/favorite/{spu_id} [post]
func (h *Handler) AddFavorite(c echo.Context) error {
	var req AddFavoriteRequest
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

	result, err := h.biz.Call().AddFavorite(c.Request().Context(), accountbiz.AddFavoriteParams{
		Account: claims.Account,
		SpuID:   req.SpuID,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}

type RemoveFavoriteRequest struct {
	SpuID uuid.UUID `param:"spu_id" validate:"required"`
}

// RemoveFavorite unfavorites an SPU for the caller.
//
//	@Summary	Remove favorite
//	@Tags		account
//	@Produce	json
//	@Security	BearerAuth
//	@Param		spu_id	path		string	true	"SPU ID (UUID)"
//	@Success	200		{object}	response.CommonResponse{data=string}
//	@Failure	401		{object}	response.CommonResponse
//	@Router		/account/favorite/{spu_id} [delete]
func (h *Handler) RemoveFavorite(c echo.Context) error {
	var req RemoveFavoriteRequest
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

	if err := h.biz.Call().RemoveFavorite(c.Request().Context(), accountbiz.RemoveFavoriteParams{
		Account: claims.Account,
		SpuID:   req.SpuID,
	}); err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromMessage(c.Response().Writer, http.StatusOK, "Favorite removed successfully")
}

type ListFavoriteRequest struct {
	paginate.Params
}

// ListFavorite returns the caller's favorited SPUs (paginated).
//
//	@Summary	List favorites
//	@Tags		account
//	@Produce	json
//	@Security	BearerAuth
//	@Param		page	query		int		false	"Page number (offset mode)"	minimum(1)
//	@Param		limit	query		int		false	"Items per page (max 100)"	minimum(1)	maximum(100)
//	@Param		cursor	query		string	false	"Keyset cursor (cursor mode)"
//	@Param		sort	query		string	false	"Sort, e.g. -date_created"
//	@Success	200		{object}	response.SwaggerPaginationResponse{data=[]accountdb.AccountFavorite}
//	@Failure	401		{object}	response.CommonResponse
//	@Router		/account/favorite [get]
func (h *Handler) ListFavorite(c echo.Context) error {
	var req ListFavoriteRequest
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

	result, err := h.biz.ListFavorite(c.Request().Context(), accountbiz.ListFavoriteParams{
		Account: claims.Account,
		Params:  req.Params,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromPaginate(c.Response().Writer, result)
}
