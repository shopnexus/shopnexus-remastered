package orderecho

import (
	"net/http"

	orderbiz "shopnexus-server/internal/module/order/biz"
	authclaims "shopnexus-server/internal/shared/claims"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/response"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// --- Buyer: create refund ---

type CreateBuyerRefundRequest struct {
	OrderID      uuid.UUID   `json:"order_id"      validate:"required"`
	Reason       string      `json:"reason"        validate:"required,min=1,max=1000"`
	ResourceIDs  []uuid.UUID `json:"resource_ids"  validate:"required,min=1,max=20,dive"`
	ReturnOption string      `json:"return_option" validate:"required,min=1,max=100"`
}

// CreateBuyerRefund opens a refund request for the buyer.
//
//	@Summary	Create buyer refund
//	@Tags		order
//	@Accept		json
//	@Produce	json
//	@Security	BearerAuth
//	@Param		body	body		CreateBuyerRefundRequest	true	"Refund payload"
//	@Success	200		{object}	response.CommonResponse{data=ordermodel.Refund}
//	@Failure	400		{object}	response.CommonResponse
//	@Failure	401		{object}	response.CommonResponse
//	@Router		/order/buyer/refund [post]
func (h *Handler) CreateBuyerRefund(c echo.Context) error {
	var req CreateBuyerRefundRequest
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

	result, err := h.biz.Call().CreateBuyerRefund(c.Request().Context(), orderbiz.CreateBuyerRefundParams{
		Account:      claims.Account,
		OrderID:      req.OrderID,
		Reason:       req.Reason,
		ResourceIDs:  req.ResourceIDs,
		ReturnOption: req.ReturnOption,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}

// --- Buyer + seller: list refunds ---

type ListBuyerRefundsRequest struct{ paginate.Params }
type ListSellerRefundsRequest struct{ paginate.Params }

// ListBuyerRefunds returns the buyer's refunds (paginated).
//
//	@Summary	List buyer refunds
//	@Tags		order
//	@Produce	json
//	@Security	BearerAuth
//	@Param		page	query		int		false	"Page number (offset mode)"	minimum(1)
//	@Param		limit	query		int		false	"Items per page (max 100)"	minimum(1)	maximum(100)
//	@Param		cursor	query		string	false	"Keyset cursor (cursor mode)"
//	@Param		sort	query		string	false	"Sort, e.g. -date_created"
//	@Success	200		{object}	response.SwaggerPaginationResponse{data=[]ordermodel.Refund}
//	@Failure	401		{object}	response.CommonResponse
//	@Router		/order/buyer/refund [get]
func (h *Handler) ListBuyerRefunds(c echo.Context) error {
	var req ListBuyerRefundsRequest
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
	result, err := h.biz.ListBuyerRefunds(c.Request().Context(), orderbiz.ListBuyerRefundsParams{
		BuyerID: claims.Account.ID,
		Params:  req.Params.Constrain(),
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromPaginate(c.Response().Writer, result)
}

// ListSellerRefunds returns the seller's refunds (paginated).
//
//	@Summary	List seller refunds
//	@Tags		order
//	@Produce	json
//	@Security	BearerAuth
//	@Param		page	query		int		false	"Page number (offset mode)"	minimum(1)
//	@Param		limit	query		int		false	"Items per page (max 100)"	minimum(1)	maximum(100)
//	@Param		cursor	query		string	false	"Keyset cursor (cursor mode)"
//	@Param		sort	query		string	false	"Sort, e.g. -date_created"
//	@Success	200		{object}	response.SwaggerPaginationResponse{data=[]ordermodel.Refund}
//	@Failure	401		{object}	response.CommonResponse
//	@Router		/order/seller/refund [get]
func (h *Handler) ListSellerRefunds(c echo.Context) error {
	var req ListSellerRefundsRequest
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
	result, err := h.biz.ListSellerRefunds(c.Request().Context(), orderbiz.ListSellerRefundsParams{
		SellerID: claims.Account.ID,
		Params:   req.Params.Constrain(),
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromPaginate(c.Response().Writer, result)
}

// --- Buyer: withdraw refund (only while still Shipping) ---

// WithdrawBuyerRefund withdraws the buyer's refund (only while still shipping).
//
//	@Summary	Withdraw buyer refund
//	@Tags		order
//	@Produce	json
//	@Security	BearerAuth
//	@Param		id	path		string	true	"Refund ID (UUID)"
//	@Success	200	{object}	response.CommonResponse{data=ordermodel.Refund}
//	@Failure	400	{object}	response.CommonResponse
//	@Failure	401	{object}	response.CommonResponse
//	@Router		/order/refunds/{id}/withdraw [post]
func (h *Handler) WithdrawBuyerRefund(c echo.Context) error {
	refundID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	claims, err := authclaims.GetClaims(c.Request())
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusUnauthorized, err)
	}
	result, err := h.biz.Call().WithdrawBuyerRefund(c.Request().Context(), orderbiz.WithdrawBuyerRefundParams{
		Account:  claims.Account,
		RefundID: refundID,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}

// --- Seller: approve refund ---

// SellerApproveRefund approves a refund request as the seller.
//
//	@Summary	Seller approve refund
//	@Tags		order
//	@Produce	json
//	@Security	BearerAuth
//	@Param		id	path		string	true	"Refund ID (UUID)"
//	@Success	200	{object}	response.CommonResponse{data=ordermodel.Refund}
//	@Failure	400	{object}	response.CommonResponse
//	@Failure	401	{object}	response.CommonResponse
//	@Router		/order/refunds/{id}/approve [post]
func (h *Handler) SellerApproveRefund(c echo.Context) error {
	refundID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	claims, err := authclaims.GetClaims(c.Request())
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusUnauthorized, err)
	}
	result, err := h.biz.Call().SellerApproveRefund(c.Request().Context(), orderbiz.SellerActionParams{
		Account:  claims.Account,
		RefundID: refundID,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}

// --- Seller: dispute refund ---

type SellerDisputeRefundRequest struct {
	Reason      string      `json:"reason"       validate:"required,min=1,max=1000"`
	ResourceIDs []uuid.UUID `json:"resource_ids" validate:"required,min=1,max=20,dive"`
}

// SellerDisputeRefund escalates a refund to an admin dispute as the seller.
//
//	@Summary	Seller dispute refund
//	@Tags		order
//	@Accept		json
//	@Produce	json
//	@Security	BearerAuth
//	@Param		id		path		string						true	"Refund ID (UUID)"
//	@Param		body	body		SellerDisputeRefundRequest	true	"Dispute payload"
//	@Success	200		{object}	response.CommonResponse{data=ordermodel.RefundDispute}
//	@Failure	400		{object}	response.CommonResponse
//	@Failure	401		{object}	response.CommonResponse
//	@Router		/order/refunds/{id}/dispute [post]
func (h *Handler) SellerDisputeRefund(c echo.Context) error {
	refundID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	var req SellerDisputeRefundRequest
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
	result, err := h.biz.Call().SellerDisputeRefund(c.Request().Context(), orderbiz.SellerDisputeParams{
		Account:     claims.Account,
		RefundID:    refundID,
		Reason:      req.Reason,
		ResourceIDs: req.ResourceIDs,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}
