package orderecho

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"shopnexus-server/internal/infras/ratelimit"
	orderbiz "shopnexus-server/internal/module/order/biz"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	"shopnexus-server/internal/provider/transport"
	authclaims "shopnexus-server/internal/shared/claims"
	sharedmodel "shopnexus-server/internal/shared/model"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/response"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/labstack/echo/v4"
)

// Handler handles HTTP requests for the order module.
type Handler struct {
	biz         orderbiz.OrderBizClient
	checkout    orderbiz.CheckoutWfClient
	fulfillment orderbiz.FulfillmentWfClient
}

// NewHandler registers order module routes and returns the handler.
func NewHandler(
	e *echo.Echo,
	biz orderbiz.OrderBizClient,
	handler *orderbiz.OrderHandler,
	rl *ratelimit.Factory,
	checkoutWf orderbiz.CheckoutWfClient,
	fulfillmentWf orderbiz.FulfillmentWfClient,
) *Handler {
	h := &Handler{
		biz:         biz,
		checkout:    checkoutWf,
		fulfillment: fulfillmentWf,
	}
	g := e.Group("/api/v1/order")

	rlCheckout := rl.Middleware("checkout", 10, time.Minute)
	rlRefund := rl.Middleware("refund", 5, time.Minute)
	rlDispute := rl.Middleware("dispute", 3, time.Minute)

	// Cart (unchanged)
	g.GET("/cart", h.GetCart)
	g.POST("/cart", h.UpdateCart)
	g.DELETE("/cart", h.ClearCart)

	// Buyer - Pending
	g.POST("/buyer/quote-transport", h.QuoteBuyerTransport)
	g.POST("/buyer/checkout", h.BuyerCheckout, rlCheckout)
	g.POST("/buyer/checkout/:sessionID/cancel", h.CancelBuyerCheckout)
	g.POST("/buyer/checkout/:sessionID/payment-url", h.EnsureBuyerCheckoutPaymentURL, rlCheckout)
	g.GET("/buyer/checkout-summary/:txID", h.GetCheckoutSummary)
	g.GET("/buyer/pending-items", h.ListBuyerPendingItems)
	g.GET("/buyer/pending-orders", h.ListBuyerPendingOrders)
	g.DELETE("/buyer/pending-items/:id", h.CancelBuyerPending)

	// Buyer - Completed
	g.GET("/buyer/completed-orders", h.ListBuyerCompletedOrders)

	// Buyer - Cancelled
	g.GET("/buyer/cancelled-items", h.ListBuyerCancelledItems)
	g.GET("/buyer/cancelled-orders", h.ListBuyerCancelledOrders)

	// Buyer - Order detail
	g.GET("/buyer/orders/:id", h.GetBuyerOrder)

	// Buyer - Refund
	buyerRefund := g.Group("/buyer/refund")
	buyerRefund.GET("", h.ListBuyerRefunds)
	buyerRefund.POST("", h.CreateBuyerRefund, rlRefund)

	// Seller - Pending
	// TODO: add casbin role middleware for /seller/* routes
	g.GET("/seller/pending", h.ListSellerPendingItems)
	g.POST("/seller/pending/confirm", h.ConfirmSellerPending)
	g.POST("/seller/pending/confirm/:sessionID/cancel", h.CancelConfirmSellerPending)
	g.POST("/seller/pending/confirm/:sessionID/payment-url", h.EnsureConfirmPaymentURL, rlCheckout)
	g.POST("/seller/pending/reject", h.RejectSellerPending)

	// Seller - Confirmed
	g.GET("/seller/confirmed", h.ListSellerConfirmed)
	g.GET("/seller/confirmed/:id", h.GetSellerOrder)

	// Seller - Refund
	g.GET("/seller/refund", h.ListSellerRefunds)

	// Refund v2 — seller decides (or auto-accept), may dispute to admin
	refund := g.Group("/refunds/:id")
	refund.POST("/approve", h.SellerApproveRefund, rlRefund)
	refund.POST("/dispute", h.SellerDisputeRefund, rlDispute)
	refund.POST("/withdraw", h.WithdrawBuyerRefund, rlRefund)

	// Dispute listing + admin resolution
	g.GET("/disputes", h.ListRefundDisputes)
	g.GET("/disputes/:disputeID", h.GetRefundDispute)
	g.GET("/refunds/:refundID/disputes", h.ListRefundDisputesByRefund)
	// Admin-only — biz layer rejects with ORDER_ADMIN_REQUIRED for non-admin callers.
	g.POST("/disputes/:disputeID/uphold", h.AdminUpholdDispute, rlDispute)
	g.POST("/disputes/:disputeID/dismiss", h.AdminDismissDispute, rlDispute)

	// registered tracks webhook idempotency keys returned by WireWebhooks so
	// providers that share an endpoint (e.g., GHTK express/standard/economy)
	// only mount their route once.
	registered := make(map[string]struct{})

	opts, err := handler.GetOptions(context.Background(), orderbiz.GetOptionsParams{Type: sharedmodel.OptionTypePayment})
	if err != nil {
		panic(fmt.Errorf("load payment options: %w", err))
	}
	for _, opt := range opts {
		client := newPaymentClient(opt)
		if client == nil {
			continue
		}
		if key := client.WireWebhooks(e, h.biz.OnPaymentResult, registered); key != "" {
			registered[key] = struct{}{}
		}
	}

	// Transport webhooks — use OrderStatus (not OrderTransportStatus)
	onTransportResult := func(ctx context.Context, result transport.WebhookResult) error {
		data, err := json.Marshal(result.Data)
		if err != nil {
			return fmt.Errorf("marshal transport webhook data: %w", err)
		}
		return biz.OnTransportResult(ctx, orderbiz.OnTransportResultParams{
			TrackingID: result.TransportID,
			Status:     orderdb.OrderStatus(result.Status),
			Data:       data,
		})
	}
	transportOpts, err := handler.GetOptions(context.Background(), orderbiz.GetOptionsParams{Type: sharedmodel.OptionTypeTransport})
	if err != nil {
		panic(fmt.Errorf("load transport options: %w", err))
	}
	for _, opt := range transportOpts {
		client := newTransportClient(opt)
		if client == nil {
			continue
		}
		if key := client.WireWebhooks(e, onTransportResult, registered); key != "" {
			registered[key] = struct{}{}
		}
	}

	return h
}

// --- Buyer Order ---

type GetBuyerOrderRequest struct {
	ID uuid.UUID `param:"id" validate:"required"`
}

func (h *Handler) GetBuyerOrder(c echo.Context) error {
	var req GetBuyerOrderRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	if _, err := authclaims.GetClaims(c.Request()); err != nil {
		return response.FromError(c.Response().Writer, http.StatusUnauthorized, err)
	}

	result, err := h.biz.GetBuyerOrder(c.Request().Context(), req.ID)
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}

type GetCheckoutSummaryRequest struct {
	TxID uuid.UUID `param:"txID" validate:"required"`
}

func (h *Handler) GetCheckoutSummary(c echo.Context) error {
	var req GetCheckoutSummaryRequest
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

	result, err := h.biz.GetCheckoutSummary(c.Request().Context(), orderbiz.GetCheckoutSummaryParams{
		AccountID: claims.Account.ID,
		TxID:      req.TxID,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}

type GetSellerOrderRequest struct {
	ID uuid.UUID `param:"id" validate:"required"`
}

func (h *Handler) GetSellerOrder(c echo.Context) error {
	var req GetSellerOrderRequest
	if err := c.Bind(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if err := c.Validate(&req); err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	if _, err := authclaims.GetClaims(c.Request()); err != nil {
		return response.FromError(c.Response().Writer, http.StatusUnauthorized, err)
	}
	result, err := h.biz.GetSellerOrder(c.Request().Context(), req.ID)
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}

type ListSellerConfirmedRequest struct {
	Search null.String `query:"search"`
	paginate.Params
}

func (h *Handler) ListSellerConfirmed(c echo.Context) error {
	var req ListSellerConfirmedRequest
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

	result, err := h.biz.ListSellerConfirmed(c.Request().Context(), orderbiz.ListSellerConfirmedParams{
		SellerID: claims.Account.ID,
		Search:   req.Search,
		Params:   req.Params.Constrain(),
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromPaginate(c.Response().Writer, result)
}

// --- Buyer Quote Transport ---

// QuoteBuyerTransportRequest mirrors BuyerCheckoutRequest minus the payment
// fields — the preview only needs cart contents + destination to compute the
// per-item shipping cost.
type QuoteBuyerTransportRequest struct {
	Address string                `json:"address" validate:"required,min=1,max=500"`
	Items   []CheckoutItemRequest `json:"items" validate:"required,min=1,dive"`
}

// QuoteBuyerTransport returns per-item shipping cost previews so the cart
// summary can show a real shipping total before the buyer submits checkout.
func (h *Handler) QuoteBuyerTransport(c echo.Context) error {
	var req QuoteBuyerTransportRequest
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

	items := make([]orderbiz.CheckoutItem, 0, len(req.Items))
	for _, item := range req.Items {
		items = append(items, orderbiz.CheckoutItem{
			SkuID:           item.SkuID,
			Quantity:        item.Quantity,
			TransportOption: item.TransportOption,
			Note:            item.Note,
		})
	}

	result, err := h.biz.QuoteTransport(c.Request().Context(), orderbiz.QuoteTransportParams{
		Account: claims.Account,
		Address: req.Address,
		Items:   items,
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromDTO(c.Response().Writer, http.StatusOK, result)
}

// --- Buyer Checkout ---

type BuyerCheckoutRequest struct {
	BuyNow        bool                  `json:"buy_now" validate:"omitempty"`
	Address       string                `json:"address" validate:"required,min=1,max=500"`
	PaymentOption string                `json:"payment_option" validate:"max=100"`
	UseWallet     bool                  `json:"use_wallet"`
	WalletID      *uuid.UUID            `json:"wallet_id,omitempty"`
	Items         []CheckoutItemRequest `json:"items"   validate:"required,min=1,dive"`
}

type CheckoutItemRequest struct {
	SkuID           uuid.UUID `json:"sku_id"           validate:"required"`
	Quantity        int64     `json:"quantity"          validate:"required,gt=0"`
	TransportOption string    `json:"transport_option"  validate:"required,min=1,max=100"`
	Note            string    `json:"note"              validate:"max=500"`
}

// BuyerCheckoutResponse is the sync envelope returned by /buyer/checkout. The
// session ID doubles as the workflow ID and the payment-gateway RefID, so
// clients can poll/cancel against the same key. PaymentURL is empty for
// wallet-only checkouts (no gateway redirect needed).
type BuyerCheckoutResponse struct {
	CheckoutSessionID string `json:"checkout_session_id"`
	PaymentURL        string `json:"payment_url"`
}

// BuyerCheckout submits a CheckoutWorkflow and synchronously attaches to its
// shared WaitPaymentURL handler so the response carries the gateway redirect
// (or empty for wallet-only). The workflow continues running asynchronously
// after this handler returns; the buyer can later cancel via
// /buyer/checkout/:sessionID/cancel which signals CancelCheckout.
func (h *Handler) BuyerCheckout(c echo.Context) error {
	var req BuyerCheckoutRequest
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

	items := make([]orderbiz.CheckoutItem, 0, len(req.Items))
	for _, item := range req.Items {
		items = append(items, orderbiz.CheckoutItem{
			SkuID:           item.SkuID,
			Quantity:        item.Quantity,
			TransportOption: item.TransportOption,
			Note:            item.Note,
		})
	}

	workflowID := uuid.New()
	input := orderbiz.CheckoutWorkflowInput{
		Account:       claims.Account,
		Items:         items,
		Address:       req.Address,
		BuyNow:        req.BuyNow,
		UseWallet:     req.UseWallet,
		WalletID:      req.WalletID,
		PaymentOption: req.PaymentOption,
	}

	ctx := c.Request().Context()

	// Submit Run as fire-and-forget — Restate journal owns the lifecycle from
	// here. We don't wait for Run() to return; instead we attach to the shared
	// WaitPaymentURL promise which Run() resolves once the gateway URL is known.
	if err := h.checkout.Send().Run(ctx, workflowID, input); err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	url, err := h.checkout.WaitPaymentURL(ctx, workflowID)
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, BuyerCheckoutResponse{
		CheckoutSessionID: workflowID.String(),
		PaymentURL:        url,
	})
}

// EnsurePaymentURLResponse is the envelope for the multi-attempt
// payment-URL endpoints. Same shape for buyer checkout + seller confirm.
type EnsurePaymentURLResponse struct {
	PaymentURL string `json:"payment_url"`
}

// EnsureBuyerCheckoutPaymentURL is the multi-attempt entry point. Returns
// the latest reusable gateway URL when the current attempt is still alive,
// otherwise signals CheckoutWorkflow.RequestNewPaymentURL to mint the next
// attempt and waits for its URL. 410 if the session is already terminal.
func (h *Handler) EnsureBuyerCheckoutPaymentURL(c echo.Context) error {
	sessionID, err := uuid.Parse(c.Param("sessionID"))
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, fmt.Errorf("invalid session id: %w", err))
	}

	if _, err := authclaims.GetClaims(c.Request()); err != nil {
		return response.FromError(c.Response().Writer, http.StatusUnauthorized, err)
	}

	ctx := c.Request().Context()

	state, err := h.biz.GetReusableGatewayURL(ctx, sessionID)
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	if state.SessionTerminated {
		return response.FromError(c.Response().Writer, http.StatusGone, fmt.Errorf("checkout session is no longer active"))
	}
	if state.ReusableURL != "" {
		return response.FromDTO(c.Response().Writer, http.StatusOK, EnsurePaymentURLResponse{PaymentURL: state.ReusableURL})
	}

	url, err := h.checkout.RequestNewPaymentURL(ctx, sessionID)
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromDTO(c.Response().Writer, http.StatusOK, EnsurePaymentURLResponse{PaymentURL: url})
}

// CancelBuyerCheckout signals CheckoutWorkflow.CancelCheckout
func (h *Handler) CancelBuyerCheckout(c echo.Context) error {
	sessionID, err := uuid.Parse(c.Param("sessionID"))
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, fmt.Errorf("invalid session id: %w", err))
	}

	if _, err := authclaims.GetClaims(c.Request()); err != nil {
		return response.FromError(c.Response().Writer, http.StatusUnauthorized, err)
	}

	if err := h.checkout.Send().CancelCheckout(c.Request().Context(), sessionID); err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromMessage(c.Response().Writer, http.StatusOK, "Checkout cancelled")
}

// --- Buyer Pending Items ---

type ListBuyerPendingItemsRequest struct {
	paginate.Params
}

func (h *Handler) ListBuyerPendingItems(c echo.Context) error {
	var req ListBuyerPendingItemsRequest
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

	result, err := h.biz.ListBuyerPendingItems(c.Request().Context(), orderbiz.ListBuyerPendingItemsParams{
		AccountID: claims.Account.ID,
		Params:    req.Params.Constrain(),
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromPaginate(c.Response().Writer, result)
}

func (h *Handler) CancelBuyerPending(c echo.Context) error {
	idStr := c.Param("id")
	itemID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}

	claims, err := authclaims.GetClaims(c.Request())
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusUnauthorized, err)
	}

	if err := h.biz.CancelBuyerPending(c.Request().Context(), orderbiz.CancelBuyerPendingParams{
		AccountID: claims.Account.ID,
		ItemID:    itemID,
	}); err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	return response.FromMessage(c.Response().Writer, http.StatusOK, "Item cancelled successfully")
}

// --- Buyer Pending Orders ---

type ListBuyerPendingOrdersRequest struct {
	paginate.Params
}

func (h *Handler) ListBuyerPendingOrders(c echo.Context) error {
	var req ListBuyerPendingOrdersRequest
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
	result, err := h.biz.ListBuyerPendingOrders(c.Request().Context(), orderbiz.ListBuyerPendingOrdersParams{
		BuyerID: claims.Account.ID,
		Params:  req.Params.Constrain(),
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromPaginate(c.Response().Writer, result)
}

// --- Buyer Completed Orders ---

type ListBuyerCompletedOrdersRequest struct {
	paginate.Params
}

func (h *Handler) ListBuyerCompletedOrders(c echo.Context) error {
	var req ListBuyerCompletedOrdersRequest
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
	result, err := h.biz.ListBuyerCompletedOrders(c.Request().Context(), orderbiz.ListBuyerCompletedOrdersParams{
		BuyerID: claims.Account.ID,
		Params:  req.Params.Constrain(),
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromPaginate(c.Response().Writer, result)
}

// --- Buyer Cancelled Orders ---

type ListBuyerCancelledOrdersRequest struct {
	paginate.Params
}

func (h *Handler) ListBuyerCancelledOrders(c echo.Context) error {
	var req ListBuyerCancelledOrdersRequest
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
	result, err := h.biz.ListBuyerCancelledOrders(c.Request().Context(), orderbiz.ListBuyerCancelledOrdersParams{
		BuyerID: claims.Account.ID,
		Params:  req.Params.Constrain(),
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromPaginate(c.Response().Writer, result)
}

// --- Buyer Cancelled Items ---

type ListBuyerCancelledItemsRequest struct {
	paginate.Params
}

func (h *Handler) ListBuyerCancelledItems(c echo.Context) error {
	var req ListBuyerCancelledItemsRequest
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
	result, err := h.biz.ListBuyerCancelledItems(c.Request().Context(), orderbiz.ListBuyerCancelledItemsParams{
		AccountID: claims.Account.ID,
		Params:    req.Params.Constrain(),
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}
	return response.FromPaginate(c.Response().Writer, result)
}
