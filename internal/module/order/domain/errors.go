package domain

import (
	"net/http"

	"shopnexus/internal/shared/errx"
)

// Order errors — every 4xx this module produces. Not-found lives here so the postgres
// adapter can return it without importing the module root.
var (
	// --- cart ---
	ErrCartItemNotFound = errx.NewError(http.StatusNotFound, "cart_item_not_found", "cart item not found")

	// --- drafts and checkout ---
	ErrDraftNotFound = errx.NewError(http.StatusNotFound, "draft_not_found", "purchase session not found")
	ErrDraftExpired  = errx.NewError(http.StatusConflict, "draft_expired", "this purchase session has expired")
	ErrDraftSettled  = errx.NewError(http.StatusConflict, "draft_settled", "this purchase session is already cancelled or checked out")
	// ErrNegotiableNeedsOffer is a checkout of a listing priced `negotiable`. It cannot be
	// bought from the listing page: the price is agreed in a negotiation first.
	ErrNegotiableNeedsOffer = errx.NewError(http.StatusUnprocessableEntity, "negotiable_needs_offer", "a negotiable listing is bought through an accepted offer")
	ErrPriceMoved           = errx.NewError(http.StatusConflict, "price_moved", "the frozen terms no longer match the listing")
	ErrCurrencyMismatch     = errx.NewError(http.StatusUnprocessableEntity, "currency_mismatch", "the currency does not match the listing's")
	ErrVariantNotInDraft    = errx.NewError(http.StatusUnprocessableEntity, "variant_not_in_draft", "that variant is not in this purchase session")

	// --- items ---
	ErrItemNotFound = errx.NewError(http.StatusNotFound, "item_not_found", "item not found")
	// ErrItemNotCancellable is a line that has already been paid into an order: from there
	// the buyer asks for a refund, which is a decision the seller gets to see.
	ErrItemNotCancellable = errx.NewError(http.StatusConflict, "item_not_cancellable", "this line is already part of an order")

	// --- offers ---
	ErrOfferNotFound = errx.NewError(http.StatusNotFound, "offer_not_found", "offer not found")
	ErrOfferSettled  = errx.NewError(http.StatusConflict, "offer_settled", "this negotiation is no longer active")
	ErrOfferExpired  = errx.NewError(http.StatusConflict, "offer_expired", "this negotiation has expired")
	// ErrOfferAlreadyOpen is one active negotiation per (buyer, variant): the terms are
	// revised in place, so a second row would be two answers to the same question.
	ErrOfferAlreadyOpen = errx.NewError(http.StatusConflict, "offer_already_open", "a negotiation on this variant is already open")
	// ErrNotYourTurn is countering your own standing proposal. The two sides alternate, so
	// a price on the table is always the other party's to answer.
	ErrNotYourTurn       = errx.NewError(http.StatusForbidden, "not_your_turn", "the standing proposal is yours; wait for a reply")
	ErrOnlyBuyerAccepts  = errx.NewError(http.StatusForbidden, "only_buyer_accepts", "only the buyer closes a negotiation")
	ErrFixedPriceListing = errx.NewError(http.StatusUnprocessableEntity, "fixed_price_listing", "this listing is not negotiable")

	// --- orders ---
	ErrOrderNotFound = errx.NewError(http.StatusNotFound, "order_not_found", "order not found")
	ErrOrderSettled  = errx.NewError(http.StatusConflict, "order_settled", "this order is already completed or cancelled")
	// ErrReceiptAlreadyConfirmed is a second confirmation. The first one started the payout
	// clock and is the evidence a refund would be judged on, so it is not re-openable.
	ErrReceiptAlreadyConfirmed = errx.NewError(http.StatusConflict, "receipt_already_confirmed", "this order's receipt is already confirmed")
	ErrReceiptNeedsEvidence    = errx.NewError(http.StatusUnprocessableEntity, "receipt_needs_evidence", "confirming receipt needs at least one photo or video")
	// ErrOrderNotCancellable is cancelling something already shipped. After that the buyer
	// asks for a refund instead: a parcel in transit cannot be un-sent.
	ErrOrderNotCancellable = errx.NewError(http.StatusConflict, "order_not_cancellable", "this order has already shipped")
	ErrAttachmentNotFound  = errx.NewError(http.StatusNotFound, "attachment_not_found", "an attachment id names no confirmed resource")

	// --- refunds and disputes ---
	ErrRefundNotFound = errx.NewError(http.StatusNotFound, "refund_not_found", "refund not found")
	ErrRefundSettled  = errx.NewError(http.StatusConflict, "refund_settled", "this refund is already settled")
	// ErrRefundAlreadyOpen is a second refund on one order. A refund covers the whole
	// order, so there is nothing a second one could be about.
	ErrRefundAlreadyOpen    = errx.NewError(http.StatusConflict, "refund_already_open", "a refund on this order is already open")
	ErrRefundNotDue         = errx.NewError(http.StatusConflict, "refund_not_due", "this order is not in a state a refund can be asked for")
	ErrNotAwaitingSeller    = errx.NewError(http.StatusConflict, "not_awaiting_seller", "this refund is not waiting on the seller")
	ErrNotAwaitingBuyer     = errx.NewError(http.StatusConflict, "not_awaiting_buyer", "this refund is not waiting on the buyer")
	ErrRejectionNeedsReason = errx.NewError(http.StatusUnprocessableEntity, "rejection_needs_reason", "a rejected refund needs a reason")
	ErrDisputeNotFound      = errx.NewError(http.StatusNotFound, "dispute_not_found", "dispute not found")
	ErrDisputeSettled       = errx.NewError(http.StatusConflict, "dispute_settled", "this dispute round is already ruled")

	// --- authorization ---
	ErrNotTheBuyer       = errx.NewError(http.StatusForbidden, "not_the_buyer", "only the buyer of this order may do that")
	ErrNotTheSeller      = errx.NewError(http.StatusForbidden, "not_the_seller", "only the seller of this order may do that")
	ErrModeratorRequired = errx.NewError(http.StatusForbidden, "moderator_required", "moderator role required")
	ErrCursorInvalid     = errx.NewError(http.StatusBadRequest, "cursor_invalid", "the cursor is not one this endpoint issued")
	ErrCarrierUnknown    = errx.NewError(http.StatusUnprocessableEntity, "carrier_unknown", "no enabled transport option by that id")

	// errQuantityPositive is the shape rule the CHECK constraints also hold: a line for
	// zero of something is not a line.
	errQuantityPositive = errx.NewError(http.StatusUnprocessableEntity, "quantity_positive", "quantity must be at least one")
)
