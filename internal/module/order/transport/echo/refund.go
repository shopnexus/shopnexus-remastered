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
	OrderID      uuid.UUID                    `json:"order_id"      validate:"required"`
	Reason       string                       `json:"reason"        validate:"required,min=1,max=1000"`
	Attachments  []orderbiz.DisputeAttachment `json:"attachments"   validate:"required,min=1,max=20,dive"`
	ReturnOption string                       `json:"return_option" validate:"required,min=1,max=100"`
}

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

	result, err := h.biz.CreateBuyerRefund(c.Request().Context(), orderbiz.CreateBuyerRefundParams{
		Account:      claims.Account,
		OrderID:      req.OrderID,
		Reason:       req.Reason,
		Attachments:  req.Attachments,
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

func (h *Handler) WithdrawBuyerRefund(c echo.Context) error {
	refundID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	claims, err := authclaims.GetClaims(c.Request())
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusUnauthorized, err)
	}
	result, err := h.biz.WithdrawBuyerRefund(c.Request().Context(), orderbiz.WithdrawBuyerRefundParams{
		Account:  claims.Account,
		RefundID: refundID,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}

// --- Seller: approve refund ---

func (h *Handler) SellerApproveRefund(c echo.Context) error {
	refundID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	claims, err := authclaims.GetClaims(c.Request())
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusUnauthorized, err)
	}
	result, err := h.biz.SellerApproveRefund(c.Request().Context(), orderbiz.SellerActionParams{
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
	Reason      string                       `json:"reason"      validate:"required,min=1,max=1000"`
	Attachments []orderbiz.DisputeAttachment `json:"attachments" validate:"required,min=1,max=20,dive"`
}

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
	result, err := h.biz.SellerDisputeRefund(c.Request().Context(), orderbiz.SellerDisputeParams{
		Account:     claims.Account,
		RefundID:    refundID,
		Reason:      req.Reason,
		Attachments: req.Attachments,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}
