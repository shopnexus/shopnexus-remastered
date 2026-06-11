package orderecho

import (
	"fmt"
	"net/http"

	orderbiz "shopnexus-server/internal/module/order/biz"
	authclaims "shopnexus-server/internal/shared/claims"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/response"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

type ListSellerPendingItemsRequest struct {
	paginate.Params
}

func (h *Handler) ListSellerPendingItems(c echo.Context) error {
	var req ListSellerPendingItemsRequest
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

	result, err := h.biz.ListSellerPendingItems(c.Request().Context(), orderbiz.ListSellerPendingItemsParams{
		SellerID: claims.Account.ID,
		Params:   req.Params.Constrain(),
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromPaginate(c.Response().Writer, result)
}

type ConfirmSellerPendingRequest struct {
	ItemIDs       []int64       `json:"item_ids"       validate:"required,min=1"`
	UseWallet     bool          `json:"use_wallet"`
	PaymentOption string        `json:"payment_option" validate:"max=100"`
	WalletID      uuid.NullUUID `json:"wallet_id"`
	Note          string        `json:"note"           validate:"max=500"`
}

// ConfirmSellerPendingResponse is the sync envelope returned by
// /seller/pending/confirm. The session ID doubles as the workflow ID and the
// payment-gateway RefID. PaymentURL is empty for wallet-only confirms.
type ConfirmSellerPendingResponse struct {
	ConfirmSessionID string `json:"confirm_session_id"`
	PaymentURL       string `json:"payment_url"`
}

// ConfirmSellerPending submits a FulfillmentWorkflow and synchronously attaches
// to its shared GetPaymentURL handler. Mirrors BuyerCheckout: the workflow
// owns the saga lifecycle, we just bridge the async submit into a sync HTTP
// response so the seller's UI can redirect to the gateway (or short-circuit
// for wallet-only confirms).
func (h *Handler) ConfirmSellerPending(c echo.Context) error {
	var req ConfirmSellerPendingRequest
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

	result, err := h.biz.ConfirmSellerPending(c.Request().Context(), orderbiz.ConfirmSellerPendingParams{
		Account:       claims.Account,
		ItemIDs:       req.ItemIDs,
		UseWallet:     req.UseWallet,
		WalletID:      req.WalletID,
		PaymentOption: req.PaymentOption,
		Note:          req.Note,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, ConfirmSellerPendingResponse{
		ConfirmSessionID: result.ConfirmSessionID.String(),
		PaymentURL:       result.PaymentURL,
	})
}

// EnsureConfirmPaymentURL is the multi-attempt entry point for seller confirms.
// Mirrors EnsureBuyerCheckoutPaymentURL: the workflow's shared GetPaymentURL
// handler decides from journaled gate state (reuse / advance / terminal error;
// expired/cancelled carry their own 409 status via response.FromError).
func (h *Handler) EnsureConfirmPaymentURL(c echo.Context) error {
	sessionID, err := uuid.Parse(c.Param("sessionID"))
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, fmt.Errorf("invalid session id: %w", err))
	}

	if _, err := authclaims.GetClaims(c.Request()); err != nil {
		return response.FromError(c.Response().Writer, http.StatusUnauthorized, err)
	}

	ctx := c.Request().Context()

	url, err := h.fulfillment.GetPaymentURL(ctx, sessionID)
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromDTO(c.Response().Writer, http.StatusOK, EnsurePaymentURLResponse{PaymentURL: url})
}

// CancelConfirmSellerPending signals FulfillmentWorkflow.CancelConfirm so
// Run() unwinds through its saga compensators (rolling back any wallet hold
// and gateway-side intent).
func (h *Handler) CancelConfirmSellerPending(c echo.Context) error {
	sessionID, err := uuid.Parse(c.Param("sessionID"))
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, fmt.Errorf("invalid session id: %w", err))
	}

	if _, err := authclaims.GetClaims(c.Request()); err != nil {
		return response.FromError(c.Response().Writer, http.StatusUnauthorized, err)
	}

	if err := h.fulfillment.Send().CancelConfirm(c.Request().Context(), sessionID); err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromMessage(c.Response().Writer, http.StatusOK, "Confirm cancelled")
}

type RejectSellerPendingRequest struct {
	ItemIDs []int64 `json:"item_ids" validate:"required,min=1"`
}

func (h *Handler) RejectSellerPending(c echo.Context) error {
	var req RejectSellerPendingRequest
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

	if err := h.biz.Call().RejectSellerPending(c.Request().Context(), orderbiz.RejectSellerPendingParams{
		Account: claims.Account,
		ItemIDs: req.ItemIDs,
	}); err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromMessage(c.Response().Writer, http.StatusOK, "Items rejected successfully")
}
