package commonecho

import (
	"net/http"

	commonbiz "shopnexus-server/internal/module/common/biz"
	authclaims "shopnexus-server/internal/shared/claims"
	"shopnexus-server/internal/shared/response"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

type ListOptionRequest struct {
	Type string `query:"type" validate:"required"`
}

// ListOption returns enabled service options of the given type.
//
//	@Summary	List options
//	@Tags		common
//	@Produce	json
//	@Param		type	query		string	true	"Option type"
//	@Success	200		{object}	response.CommonResponse{data=[]commonbiz.OptionListItem}
//	@Failure	400		{object}	response.CommonResponse
//	@Router		/common/option [get]
func (h *Handler) ListOption(c echo.Context) error {
	var req ListOptionRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	// Endpoint is public; claims are best-effort. Anonymous callers get owned=false.
	var accountID uuid.NullUUID
	if claims, err := authclaims.GetClaims(c.Request()); err == nil {
		accountID = uuid.NullUUID{UUID: claims.Account.ID, Valid: true}
	}

	result, err := h.biz.ListOption(c.Request().Context(), commonbiz.ListOptionParams{
		Type:      []string{req.Type},
		IsEnabled: []bool{true},
		AccountID: accountID,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}

// UpsertOptions inserts or updates a batch of service options.
//
//	@Summary	Upsert options
//	@Tags		common
//	@Accept		json
//	@Produce	json
//	@Security	BearerAuth
//	@Param		body	body		commonbiz.UpsertOptionsParams	true	"Options to upsert"
//	@Success	200		{object}	response.CommonResponse{data=string}
//	@Failure	400		{object}	response.CommonResponse
//	@Failure	401		{object}	response.CommonResponse
//	@Router		/common/option [post]
func (h *Handler) UpsertOptions(c echo.Context) error {
	var req commonbiz.UpsertOptionsParams
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := h.biz.Call().UpsertOptions(c.Request().Context(), req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromMessage(c.Response().Writer, http.StatusOK, "Options upserted")
}

// DeleteOptions removes a batch of service options by ID.
//
//	@Summary	Delete options
//	@Tags		common
//	@Accept		json
//	@Produce	json
//	@Security	BearerAuth
//	@Param		body	body		commonbiz.DeleteOptionParams	true	"Option IDs to delete"
//	@Success	200		{object}	response.CommonResponse{data=string}
//	@Failure	400		{object}	response.CommonResponse
//	@Failure	401		{object}	response.CommonResponse
//	@Router		/common/option [delete]
func (h *Handler) DeleteOptions(c echo.Context) error {
	var req commonbiz.DeleteOptionParams
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := h.biz.Call().DeleteOptions(c.Request().Context(), req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromMessage(c.Response().Writer, http.StatusOK, "Options deleted")
}
