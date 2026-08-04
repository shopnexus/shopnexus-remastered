// Package ordertest provides a stub orderapi.Service for tests.
//
// A test that cares about one method should not have to write the rest. Embed Stub and
// override what the test is about; anything left over answers 501, so an unstubbed call shows
// up as an obviously wrong status rather than as a plausible zero value.
package ordertest

import (
	"context"

	"shopnexus/internal/module/common"
	orderapi "shopnexus/internal/module/order/api"
	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/id"
)

// Stub implements orderapi.Service by refusing everything.
type Stub struct{}

var _ orderapi.Service = Stub{}

func (Stub) ListCartItems(context.Context, orderapi.ListCartRequest) ([]orderapi.CartItem, error) {
	return nil, errx.ErrNotImplemented
}

func (Stub) AddCartItem(context.Context, orderapi.AddCartItemRequest) (orderapi.CartItem, error) {
	return orderapi.CartItem{}, errx.ErrNotImplemented
}

func (Stub) UpdateCartItem(context.Context, orderapi.UpdateCartItemRequest) (orderapi.CartItem, error) {
	return orderapi.CartItem{}, errx.ErrNotImplemented
}

func (Stub) DeleteCartItem(context.Context, orderapi.CartItemRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) CreateDraft(context.Context, orderapi.CreateDraftRequest) (orderapi.Draft, error) {
	return orderapi.Draft{}, errx.ErrNotImplemented
}

func (Stub) ListDrafts(context.Context, orderapi.ListDraftsRequest) (orderapi.DraftPage, error) {
	return orderapi.DraftPage{}, errx.ErrNotImplemented
}

func (Stub) GetDraft(context.Context, orderapi.DraftRequest) (orderapi.Draft, error) {
	return orderapi.Draft{}, errx.ErrNotImplemented
}

func (Stub) CancelDraft(context.Context, orderapi.DraftRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) Checkout(context.Context, orderapi.CheckoutRequest) (orderapi.CheckoutResult, error) {
	return orderapi.CheckoutResult{}, errx.ErrNotImplemented
}

func (Stub) ListItems(context.Context, orderapi.ListItemsRequest) (orderapi.ItemPage, error) {
	return orderapi.ItemPage{}, errx.ErrNotImplemented
}

func (Stub) CancelItem(context.Context, orderapi.ItemRequest) (orderapi.Item, error) {
	return orderapi.Item{}, errx.ErrNotImplemented
}

func (Stub) CreateOffer(context.Context, orderapi.CreateOfferRequest) (orderapi.Offer, error) {
	return orderapi.Offer{}, errx.ErrNotImplemented
}

func (Stub) ListOffers(context.Context, orderapi.ListOffersRequest) (orderapi.OfferPage, error) {
	return orderapi.OfferPage{}, errx.ErrNotImplemented
}

func (Stub) GetOffer(context.Context, orderapi.OfferRequest) (orderapi.Offer, error) {
	return orderapi.Offer{}, errx.ErrNotImplemented
}

func (Stub) CounterOffer(context.Context, orderapi.CounterOfferRequest) (orderapi.Offer, error) {
	return orderapi.Offer{}, errx.ErrNotImplemented
}

func (Stub) CancelOffer(context.Context, orderapi.OfferRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) AcceptOffer(context.Context, orderapi.OfferRequest) (orderapi.Offer, error) {
	return orderapi.Offer{}, errx.ErrNotImplemented
}

func (Stub) CheckoutOffer(context.Context, orderapi.CheckoutOfferRequest) (orderapi.CheckoutResult, error) {
	return orderapi.CheckoutResult{}, errx.ErrNotImplemented
}

func (Stub) ListOrders(context.Context, orderapi.ListOrdersRequest) (orderapi.OrderPage, error) {
	return orderapi.OrderPage{}, errx.ErrNotImplemented
}

func (Stub) GetOrder(context.Context, orderapi.OrderRequest) (orderapi.Order, error) {
	return orderapi.Order{}, errx.ErrNotImplemented
}

func (Stub) ConfirmReceipt(context.Context, orderapi.ConfirmReceiptRequest) (orderapi.Order, error) {
	return orderapi.Order{}, errx.ErrNotImplemented
}

func (Stub) CancelOrder(context.Context, orderapi.CancelOrderRequest) (orderapi.Order, error) {
	return orderapi.Order{}, errx.ErrNotImplemented
}

func (Stub) GetOrderTransport(context.Context, orderapi.OrderRequest) (orderapi.Transport, error) {
	return orderapi.Transport{}, errx.ErrNotImplemented
}

func (Stub) AdvanceShipment(context.Context, orderapi.AdvanceShipmentRequest) (orderapi.Transport, error) {
	return orderapi.Transport{}, errx.ErrNotImplemented
}

func (Stub) CreateRefund(context.Context, orderapi.CreateRefundRequest) (orderapi.Refund, error) {
	return orderapi.Refund{}, errx.ErrNotImplemented
}

func (Stub) ListRefunds(context.Context, orderapi.ListRefundsRequest) (orderapi.RefundPage, error) {
	return orderapi.RefundPage{}, errx.ErrNotImplemented
}

func (Stub) GetRefund(context.Context, orderapi.RefundRequest) (orderapi.Refund, error) {
	return orderapi.Refund{}, errx.ErrNotImplemented
}

func (Stub) WithdrawRefund(context.Context, orderapi.RefundRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) AddRefundAttachments(context.Context, orderapi.AddRefundAttachmentsRequest) (orderapi.Refund, error) {
	return orderapi.Refund{}, errx.ErrNotImplemented
}

func (Stub) AcceptRefund(context.Context, orderapi.RefundRequest) (orderapi.Refund, error) {
	return orderapi.Refund{}, errx.ErrNotImplemented
}

func (Stub) RejectRefund(context.Context, orderapi.RejectRefundRequest) (orderapi.Refund, error) {
	return orderapi.Refund{}, errx.ErrNotImplemented
}

func (Stub) AdvanceReturnShipment(context.Context, orderapi.AdvanceReturnShipmentRequest) (orderapi.Refund, error) {
	return orderapi.Refund{}, errx.ErrNotImplemented
}

func (Stub) EscalateRefund(context.Context, orderapi.EscalateRefundRequest) (orderapi.Refund, error) {
	return orderapi.Refund{}, errx.ErrNotImplemented
}

func (Stub) AdminResolveRefund(context.Context, orderapi.ResolveRefundRequest) (orderapi.Refund, error) {
	return orderapi.Refund{}, errx.ErrNotImplemented
}

func (Stub) CreateUpload(context.Context, orderapi.CreateUploadRequest) (orderapi.UploadSlot, error) {
	return orderapi.UploadSlot{}, errx.ErrNotImplemented
}

func (Stub) ConfirmUpload(context.Context, orderapi.ConfirmUploadRequest) (common.ResourceDTO, error) {
	return common.ResourceDTO{}, errx.ErrNotImplemented
}

func (Stub) SettlePaidSession(context.Context, id.ID[id.PaymentSession]) error {
	return errx.ErrNotImplemented
}

func (Stub) ExpireDrafts(context.Context, int) (int, error) {
	return 0, errx.ErrNotImplemented
}

func (Stub) ExpireCheckouts(context.Context, int) (int, error) {
	return 0, errx.ErrNotImplemented
}

func (Stub) ExpireOffers(context.Context, int) (int, error) {
	return 0, errx.ErrNotImplemented
}

func (Stub) ReleaseDuePayouts(context.Context, int) (int, error) {
	return 0, errx.ErrNotImplemented
}

func (Stub) RetryClaimedPayouts(context.Context, int) (int, error) {
	return 0, errx.ErrNotImplemented
}

func (Stub) AdvanceOverdueRefunds(context.Context, int) (int, error) {
	return 0, errx.ErrNotImplemented
}

func (Stub) ReleasePayout(context.Context, id.ID[id.Order]) error {
	return errx.ErrNotImplemented
}

func (Stub) AdvanceRefund(context.Context, id.ID[id.Refund]) error {
	return errx.ErrNotImplemented
}

func (Stub) ShippingQuotes(context.Context, orderapi.ShippingQuotesRequest) (orderapi.ShippingQuotes, error) {
	return orderapi.ShippingQuotes{}, errx.ErrNotImplemented
}
