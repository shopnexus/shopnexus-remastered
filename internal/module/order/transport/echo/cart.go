package orderecho

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"

	orderbiz "shopnexus-server/internal/module/order/biz"
	authclaims "shopnexus-server/internal/shared/claims"
	"shopnexus-server/internal/shared/response"

	"github.com/labstack/echo/v4"
)

type GetCartRequest struct {
}

// GetCart returns the caller's cart items.
//
//	@Summary	Get cart
//	@Tags		order
//	@Produce	json
//	@Security	BearerAuth
//	@Success	200	{object}	response.CommonResponse{data=[]ordermodel.CartItemView}
//	@Failure	401	{object}	response.CommonResponse
//	@Router		/order/cart [get]
func (h *Handler) GetCart(c echo.Context) error {
	var req GetCartRequest
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

	result, err := h.biz.GetCart(c.Request().Context(), orderbiz.GetCartParams{
		AccountID: claims.Account.ID,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}

type UpdateCartRequest struct {
	SkuID         uuid.UUID  `json:"sku_id"         validate:"required"`
	Quantity      null.Int64 `json:"quantity"       validate:"omitnil"`
	DeltaQuantity null.Int64 `json:"delta_quantity" validate:"omitnil"`
}

// UpdateCart sets or adjusts the quantity of a SKU in the caller's cart.
//
//	@Summary	Update cart
//	@Tags		order
//	@Accept		json
//	@Produce	json
//	@Security	BearerAuth
//	@Param		body	body		UpdateCartRequest	true	"Cart update payload"
//	@Success	200		{object}	response.CommonResponse{data=string}
//	@Failure	400		{object}	response.CommonResponse
//	@Failure	401		{object}	response.CommonResponse
//	@Router		/order/cart [post]
func (h *Handler) UpdateCart(c echo.Context) error {
	var req UpdateCartRequest
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

	if err = h.biz.Call().UpdateCart(c.Request().Context(), orderbiz.UpdateCartParams{
		Account:       claims.Account,
		SkuID:         req.SkuID,
		Quantity:      req.Quantity,
		DeltaQuantity: req.DeltaQuantity,
	}); err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromMessage(c.Response().Writer, http.StatusOK, "Update cart successfully")
}

type ClearCartRequest struct {
}

// ClearCart removes all items from the caller's cart.
//
//	@Summary	Clear cart
//	@Tags		order
//	@Produce	json
//	@Security	BearerAuth
//	@Success	200	{object}	response.CommonResponse{data=string}
//	@Failure	401	{object}	response.CommonResponse
//	@Router		/order/cart [delete]
func (h *Handler) ClearCart(c echo.Context) error {
	var req ClearCartRequest
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

	if err := h.biz.Call().ClearCart(c.Request().Context(), orderbiz.ClearCartParams{
		Account: claims.Account,
	}); err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromMessage(c.Response().Writer, http.StatusOK, "Clear cart successfully")
}
