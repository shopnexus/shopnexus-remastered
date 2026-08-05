package handler

import (
	"log/slog"
	"net/http"

	"github.com/go-playground/validator/v10"

	orderapi "shopnexus/internal/module/order/api"
	"shopnexus/internal/shared/httpx"
	"shopnexus/internal/shared/id"
)

// Order serves the order module's routes: the cart, the purchase session, the negotiation,
// the order, its shipment and its refunds.
//
// There is no route that turns paid lines into an order — the money does that — so the
// handlers here are the buyer's and the seller's decisions and nothing else.
type Order struct {
	svc orderapi.Service
	v   *validator.Validate
	log *slog.Logger
}

func NewOrder(svc orderapi.Service, v *validator.Validate, log *slog.Logger) *Order {
	return &Order{svc: svc, v: v, log: log}
}

// --- cart ---

// ListCartItems handles GET /cart-items.
func (h *Order) ListCartItems(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	req := orderapi.ListCartRequest{ActorID: uid}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.ListCartItems(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// AddCartItem handles POST /cart-items. Adding the same variant twice tops the row up rather
// than stacking: the cart is keyed by (account, variant).
func (h *Order) AddCartItem(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	var req orderapi.AddCartItemRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID = uid
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.AddCartItem(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusCreated, res)
}

// UpdateCartItem handles PATCH /cart-items/{id} — the quantity outright.
func (h *Order) UpdateCartItem(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	cartItemID, err := pathID[id.CartItem](r, "id")
	if failed(w, h.log, err) {
		return
	}
	var req orderapi.UpdateCartItemRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID, req.ID = uid, cartItemID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.UpdateCartItem(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// DeleteCartItem handles DELETE /cart-items/{id}.
func (h *Order) DeleteCartItem(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	cartItemID, err := pathID[id.CartItem](r, "id")
	if failed(w, h.log, err) {
		return
	}
	req := orderapi.CartItemRequest{ActorID: uid, ID: cartItemID}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	if failed(w, h.log, h.svc.DeleteCartItem(r.Context(), req)) {
		return
	}
	httpx.WriteNoContent(w)
}

// --- purchase sessions ---

// CreateDraft handles POST /drafts — freezing a fixed-price listing's terms.
func (h *Order) CreateDraft(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	var req orderapi.CreateDraftRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID = uid
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.CreateDraft(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusCreated, res)
}

// ListDrafts handles GET /drafts.
func (h *Order) ListDrafts(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	limit, err := limitParam(r)
	if failed(w, h.log, err) {
		return
	}
	req := orderapi.ListDraftsRequest{
		ActorID: uid, Cursor: cursorParam(r), Limit: limit,
	}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.ListDrafts(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteCursor(w, http.StatusOK, res.Data, cursorMeta(res.Meta.NextCursor))
}

// GetDraft handles GET /drafts/{id}.
func (h *Order) GetDraft(w http.ResponseWriter, r *http.Request) {
	req, err := h.draftRequest(r)
	if failed(w, h.log, err) {
		return
	}
	res, err := h.svc.GetDraft(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// CancelDraft handles DELETE /drafts/{id}.
func (h *Order) CancelDraft(w http.ResponseWriter, r *http.Request) {
	req, err := h.draftRequest(r)
	if failed(w, h.log, err) {
		return
	}
	if failed(w, h.log, h.svc.CancelDraft(r.Context(), req)) {
		return
	}
	httpx.WriteNoContent(w)
}

func (h *Order) draftRequest(r *http.Request) (orderapi.DraftRequest, error) {
	uid, err := actor(r)
	if err != nil {
		return orderapi.DraftRequest{}, err
	}
	draftID, err := pathID[id.DraftOrder](r, "id")
	if err != nil {
		return orderapi.DraftRequest{}, err
	}
	req := orderapi.DraftRequest{ActorID: uid, ID: draftID}
	return req, check(h.v, req)
}

// Checkout handles POST /drafts/{id}/checkout. 201 for the lines and the session; the order
// follows when the money lands, which has no route of its own.
func (h *Order) Checkout(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	draftID, err := pathID[id.DraftOrder](r, "id")
	if failed(w, h.log, err) {
		return
	}
	var req orderapi.CheckoutRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID, req.ID = uid, draftID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.Checkout(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusCreated, res)
}

// --- lines ---

// ListItems handles GET /items — "my purchases", or what a seller is shipping.
func (h *Order) ListItems(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	limit, err := limitParam(r)
	if failed(w, h.log, err) {
		return
	}
	pending, err := boolParam(r, "pending")
	if failed(w, h.log, err) {
		return
	}
	req := orderapi.ListItemsRequest{
		ActorID: uid,
		Role:    r.URL.Query().Get("role"),
		Cursor:  cursorParam(r),
		Limit:   limit,
	}
	if pending != nil {
		req.Pending = *pending
	}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.ListItems(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteCursor(w, http.StatusOK, res.Data, cursorMeta(res.Meta.NextCursor))
}

// CancelItem handles POST /items/{id}/cancellation — before the money lands. After that the
// buyer asks for a refund instead.
func (h *Order) CancelItem(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	itemID, err := pathID[id.Item](r, "id")
	if failed(w, h.log, err) {
		return
	}
	req := orderapi.ItemRequest{ActorID: uid, ID: itemID}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.CancelItem(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// --- negotiations ---

// CreateOffer handles POST /offers — opening a negotiation on a negotiable listing.
func (h *Order) CreateOffer(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	var req orderapi.CreateOfferRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID = uid
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.CreateOffer(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusCreated, res)
}

// ListOffers handles GET /offers.
func (h *Order) ListOffers(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	limit, err := limitParam(r)
	if failed(w, h.log, err) {
		return
	}
	req := orderapi.ListOffersRequest{
		ActorID: uid,
		Status:  r.URL.Query().Get("status"),
		Cursor:  cursorParam(r),
		Limit:   limit,
	}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.ListOffers(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteCursor(w, http.StatusOK, res.Data, cursorMeta(res.Meta.NextCursor))
}

// GetOffer handles GET /offers/{id}.
func (h *Order) GetOffer(w http.ResponseWriter, r *http.Request) {
	req, err := h.offerRequest(r)
	if failed(w, h.log, err) {
		return
	}
	res, err := h.svc.GetOffer(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// CounterOffer handles PATCH /offers/{id} — revising the terms and handing the turn over.
func (h *Order) CounterOffer(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	offerID, err := pathID[id.Offer](r, "id")
	if failed(w, h.log, err) {
		return
	}
	var req orderapi.CounterOfferRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID, req.ID = uid, offerID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.CounterOffer(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// CancelOffer handles DELETE /offers/{id}.
func (h *Order) CancelOffer(w http.ResponseWriter, r *http.Request) {
	req, err := h.offerRequest(r)
	if failed(w, h.log, err) {
		return
	}
	if failed(w, h.log, h.svc.CancelOffer(r.Context(), req)) {
		return
	}
	httpx.WriteNoContent(w)
}

func (h *Order) offerRequest(r *http.Request) (orderapi.OfferRequest, error) {
	uid, err := actor(r)
	if err != nil {
		return orderapi.OfferRequest{}, err
	}
	offerID, err := pathID[id.Offer](r, "id")
	if err != nil {
		return orderapi.OfferRequest{}, err
	}
	req := orderapi.OfferRequest{ActorID: uid, ID: offerID}
	return req, check(h.v, req)
}

// AcceptOffer handles POST /offers/{id}/acceptance — agreeing to the terms on the table. Either
// party may, whichever of them does not own the standing proposal; nothing is charged.
func (h *Order) AcceptOffer(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	offerID, err := pathID[id.Offer](r, "id")
	if failed(w, h.log, err) {
		return
	}
	req := orderapi.OfferRequest{ActorID: uid, ID: offerID}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.AcceptOffer(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// ShippingQuotes handles POST /shipping-quotes — what delivery would cost, per carrier, for a
// draft or for agreed terms. One route for both, because the buyer pays carriage either way and
// the page they choose from is the same.
func (h *Order) ShippingQuotes(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	var req orderapi.ShippingQuotesRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID = uid
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.ShippingQuotes(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// CheckoutOffer handles POST /offers/{id}/checkout — the buyer's "create order now" on agreed
// terms, where they choose delivery and pay, exactly as on a fixed-price listing.
func (h *Order) CheckoutOffer(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	offerID, err := pathID[id.Offer](r, "id")
	if failed(w, h.log, err) {
		return
	}
	var req orderapi.CheckoutOfferRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID, req.ID = uid, offerID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.CheckoutOffer(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusCreated, res)
}

// --- orders ---

// ListOrders handles GET /orders, as buyer or as seller.
// GetOrderSummary handles GET /orders/summary.
func (h *Order) GetOrderSummary(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	from, err := optionalTimeParam(r, "from")
	if failed(w, h.log, err) {
		return
	}
	to, err := optionalTimeParam(r, "to")
	if failed(w, h.log, err) {
		return
	}
	req := orderapi.OrderSummaryRequest{
		ActorID: uid,
		Role:    r.URL.Query().Get("role"),
		From:    from,
		To:      to,
		TZ:      r.URL.Query().Get("tz"),
	}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.GetOrderSummary(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

func (h *Order) ListOrders(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	limit, err := limitParam(r)
	if failed(w, h.log, err) {
		return
	}
	req := orderapi.ListOrdersRequest{
		ActorID: uid,
		Role:    r.URL.Query().Get("role"),
		State:   r.URL.Query().Get("state"),
		Cursor:  cursorParam(r),
		Limit:   limit,
	}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.ListOrders(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteCursor(w, http.StatusOK, res.Data, cursorMeta(res.Meta.NextCursor))
}

// GetOrder handles GET /orders/{id}.
func (h *Order) GetOrder(w http.ResponseWriter, r *http.Request) {
	req, err := h.orderRequest(r)
	if failed(w, h.log, err) {
		return
	}
	res, err := h.svc.GetOrder(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// GetOrderTransport handles GET /orders/{id}/transport.
func (h *Order) GetOrderTransport(w http.ResponseWriter, r *http.Request) {
	req, err := h.orderRequest(r)
	if failed(w, h.log, err) {
		return
	}
	res, err := h.svc.GetOrderTransport(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// AdvanceShipment handles POST /orders/{id}/transport/checkpoints — a carrier checkpoint on the
// outbound leg, reported by the seller.
func (h *Order) AdvanceShipment(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	orderID, err := pathID[id.Order](r, "id")
	if failed(w, h.log, err) {
		return
	}
	var req orderapi.AdvanceShipmentRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID, req.ID = uid, orderID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.AdvanceShipment(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

func (h *Order) orderRequest(r *http.Request) (orderapi.OrderRequest, error) {
	uid, err := actor(r)
	if err != nil {
		return orderapi.OrderRequest{}, err
	}
	orderID, err := pathID[id.Order](r, "id")
	if err != nil {
		return orderapi.OrderRequest{}, err
	}
	req := orderapi.OrderRequest{ActorID: uid, ID: orderID}
	return req, check(h.v, req)
}

// ConfirmReceipt handles POST /orders/{id}/receipt. The evidence is mandatory: a later refund
// or dispute is judged on it.
func (h *Order) ConfirmReceipt(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	orderID, err := pathID[id.Order](r, "id")
	if failed(w, h.log, err) {
		return
	}
	var req orderapi.ConfirmReceiptRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID, req.ID = uid, orderID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.ConfirmReceipt(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// CancelOrder handles POST /orders/{id}/cancellation — only before the parcel leaves.
func (h *Order) CancelOrder(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	orderID, err := pathID[id.Order](r, "id")
	if failed(w, h.log, err) {
		return
	}
	var req orderapi.CancelOrderRequest
	if failed(w, h.log, decodeOptionalBody(r, &req)) {
		return
	}
	req.ActorID, req.ID = uid, orderID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.CancelOrder(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// --- refunds ---

// CreateRefund handles POST /orders/{id}/refunds.
func (h *Order) CreateRefund(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	orderID, err := pathID[id.Order](r, "id")
	if failed(w, h.log, err) {
		return
	}
	var req orderapi.CreateRefundRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID, req.OrderID = uid, orderID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.CreateRefund(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusCreated, res)
}

// ListRefunds handles GET /refunds.
func (h *Order) ListRefunds(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	limit, err := limitParam(r)
	if failed(w, h.log, err) {
		return
	}
	req := orderapi.ListRefundsRequest{
		ActorID: uid,
		Role:    r.URL.Query().Get("role"),
		Status:  r.URL.Query().Get("status"),
		Cursor:  cursorParam(r),
		Limit:   limit,
	}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.ListRefunds(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteCursor(w, http.StatusOK, res.Data, cursorMeta(res.Meta.NextCursor))
}

// GetRefund handles GET /refunds/{id}.
func (h *Order) GetRefund(w http.ResponseWriter, r *http.Request) {
	req, err := h.refundRequest(r)
	if failed(w, h.log, err) {
		return
	}
	res, err := h.svc.GetRefund(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// WithdrawRefund handles DELETE /refunds/{id} — the buyer dropping it before a verdict.
func (h *Order) WithdrawRefund(w http.ResponseWriter, r *http.Request) {
	req, err := h.refundRequest(r)
	if failed(w, h.log, err) {
		return
	}
	if failed(w, h.log, h.svc.WithdrawRefund(r.Context(), req)) {
		return
	}
	httpx.WriteNoContent(w)
}

// AcceptRefund handles POST /refunds/{id}/acceptance — the seller granting it, which opens
// the return leg.
func (h *Order) AcceptRefund(w http.ResponseWriter, r *http.Request) {
	req, err := h.refundRequest(r)
	if failed(w, h.log, err) {
		return
	}
	res, err := h.svc.AcceptRefund(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

func (h *Order) refundRequest(r *http.Request) (orderapi.RefundRequest, error) {
	uid, err := actor(r)
	if err != nil {
		return orderapi.RefundRequest{}, err
	}
	refundID, err := pathID[id.Refund](r, "id")
	if err != nil {
		return orderapi.RefundRequest{}, err
	}
	req := orderapi.RefundRequest{ActorID: uid, ID: refundID}
	return req, check(h.v, req)
}

// AddRefundAttachments handles POST /refunds/{id}/attachments — topping up the evidence while
// the case is open.
func (h *Order) AddRefundAttachments(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	refundID, err := pathID[id.Refund](r, "id")
	if failed(w, h.log, err) {
		return
	}
	var req orderapi.AddRefundAttachmentsRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID, req.ID = uid, refundID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.AddRefundAttachments(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// RejectRefund handles POST /refunds/{id}/rejection. The reason is required: the buyer is
// owed the why, and it is what separates a refusal from a seller who said nothing.
func (h *Order) RejectRefund(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	refundID, err := pathID[id.Refund](r, "id")
	if failed(w, h.log, err) {
		return
	}
	var req orderapi.RejectRefundRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID, req.ID = uid, refundID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.RejectRefund(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// AdvanceReturnShipment handles POST /refunds/{id}/return-transport/checkpoints. Marking it
// delivered is what opens the seller's inspection window — the only exit from `returning`.
func (h *Order) AdvanceReturnShipment(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	refundID, err := pathID[id.Refund](r, "id")
	if failed(w, h.log, err) {
		return
	}
	var req orderapi.AdvanceReturnShipmentRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID, req.ID = uid, refundID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.AdvanceReturnShipment(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// AdminResolveRefund handles POST /admin/refunds/{id}/verdict. The only staff decision on a
// refund: escalating is trust's, because that is where the ticket lives.
func (h *Order) AdminResolveRefund(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	refundID, err := pathID[id.Refund](r, "id")
	if failed(w, h.log, err) {
		return
	}
	var req orderapi.ResolveRefundRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID, req.ID = uid, refundID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.AdminResolveRefund(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// --- uploads ---

// CreateUpload handles POST /orders/uploads — a slot to PUT evidence into: the unboxing
// photos a receipt confirmation or a refund carries.
func (h *Order) CreateUpload(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	var req orderapi.CreateUploadRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID = uid
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.CreateUpload(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusCreated, res)
}

// ConfirmUpload handles POST /orders/uploads/{id}/confirmation — the bytes are at the store.
func (h *Order) ConfirmUpload(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	resourceID, err := pathID[id.Resource](r, "id")
	if failed(w, h.log, err) {
		return
	}
	req := orderapi.ConfirmUploadRequest{ActorID: uid, ID: resourceID}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.ConfirmUpload(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}
