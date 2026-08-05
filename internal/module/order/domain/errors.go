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
	ErrDraftNotFound     = errx.NewError(http.StatusNotFound, "draft_not_found", "purchase session not found")
	ErrDraftExpired      = errx.NewError(http.StatusConflict, "draft_expired", "this purchase session has expired")
	ErrDraftSettled      = errx.NewError(http.StatusConflict, "draft_settled", "this purchase session is already cancelled or checked out")
	ErrPriceMoved        = errx.NewError(http.StatusConflict, "price_moved", "the frozen terms no longer match the listing")
	ErrCurrencyMismatch  = errx.NewError(http.StatusUnprocessableEntity, "currency_mismatch", "the currency does not match the listing's")
	ErrVariantNotInDraft = errx.NewError(http.StatusUnprocessableEntity, "variant_not_in_draft", "that variant is not in this purchase session")

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
	ErrNotYourTurn = errx.NewError(http.StatusForbidden, "not_your_turn", "the standing proposal is yours; wait for a reply")
	// ErrOfferNotAccepted is a checkout of terms nobody has agreed to yet. A negotiable listing
	// cannot be bought until one side says yes to the other's price.
	ErrOfferNotAccepted = errx.NewError(http.StatusConflict, "offer_not_accepted", "these terms have not been agreed yet")
	// ErrSellerCannotOffer is the seller trying to open the negotiation. A proposal needs
	// somebody to propose to, and on their own listing there is nobody.
	ErrSellerCannotOffer = errx.NewError(http.StatusForbidden, "seller_cannot_offer", "the buyer opens a negotiation")
	// ErrOnlyBuyerCheckout is a seller pressing "create order now". Either side may agree to a
	// price; only the buyer turns it into an order, because only the buyer pays.
	ErrOnlyBuyerCheckout = errx.NewError(http.StatusForbidden, "only_buyer_checkout", "only the buyer checks out agreed terms")
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

	// --- transport ---
	// ErrTransportSettled is a checkpoint that would move a shipment backwards, or one on a
	// leg that already ended. Carrier reports arrive out of order, and `Shipped()` decides
	// whether an order can still be cancelled — so it is not a fact a later report may undo.
	ErrTransportSettled       = errx.NewError(http.StatusConflict, "transport_settled", "this shipment is already at or past that point")
	ErrTransportStatusUnknown = errx.NewError(http.StatusUnprocessableEntity, "transport_status_unknown", "no shipment status by that name")
	// ErrTransportNotFound is a shipment nobody has: a carrier reporting on a reference this
	// platform never booked, or an order whose transport row is missing. Not ErrOrderNotFound —
	// answering 404 "order not found" for an order that plainly exists sends a client hunting
	// for the wrong bug.
	ErrTransportNotFound = errx.NewError(http.StatusNotFound, "transport_not_found", "no shipment with that carrier reference")
	ErrNoReturnLeg       = errx.NewError(http.StatusConflict, "no_return_leg", "this refund has no return shipment yet")

	// --- refunds ---
	ErrRefundNotFound = errx.NewError(http.StatusNotFound, "refund_not_found", "refund not found")
	// ErrRefundSettled is a write that lost: the case is finished, or it moved while the caller held
	// an older copy of it. One answer for both, because the caller's remedy is the same — re-read.
	// A summary's window has to be a window: a dashboard that asks for one backwards, or for one
	// bucket per day since the platform opened, is a request the caller can fix.
	ErrSummaryWindowInvalid = errx.NewError(http.StatusUnprocessableEntity, "summary_window_invalid", "the window has to end after it starts")
	ErrSummaryWindowTooWide = errx.NewError(http.StatusUnprocessableEntity, "summary_window_too_wide", "a summary covers at most a year")
	ErrTimeZoneUnknown      = errx.NewError(http.StatusUnprocessableEntity, "time_zone_unknown", "no such IANA time zone")
	ErrRefundSettled        = errx.NewError(http.StatusConflict, "refund_settled", "this refund has moved on")
	// ErrRefundAlreadyOpen is a second refund on one order. A refund covers the whole
	// order, so there is nothing a second one could be about.
	ErrRefundAlreadyOpen    = errx.NewError(http.StatusConflict, "refund_already_open", "a refund on this order is already open")
	ErrRefundNotDue         = errx.NewError(http.StatusConflict, "refund_not_due", "this order is not in a state a refund can be asked for")
	ErrNotAwaitingSeller    = errx.NewError(http.StatusConflict, "not_awaiting_seller", "this refund is not waiting on the seller")
	ErrNotAwaitingBuyer     = errx.NewError(http.StatusConflict, "not_awaiting_buyer", "this refund is not waiting on the buyer")
	ErrRejectionNeedsReason = errx.NewError(http.StatusUnprocessableEntity, "rejection_needs_reason", "a rejected refund needs a reason")
	// ErrSessionPaid is cancelling a line the buyer has already paid for. The order follows
	// from the money, so undoing the sale is a refund the seller gets to see — cancelling here
	// would release the stock and leave the payment covering nothing.
	ErrSessionPaid = errx.NewError(http.StatusConflict, "session_paid", "this line is paid for; a refund is how a paid sale is undone")
	// ErrRefundNotEscalatable is asking staff to look at a case nobody is waiting on: only a
	// refused refund and a delivered return are states a party can disagree with.
	ErrRefundNotEscalatable = errx.NewError(http.StatusConflict, "refund_not_escalatable", "this refund cannot be escalated from its current state")
	// ErrRefundNotDisputed is a verdict on a case staff were never asked about.
	ErrRefundNotDisputed = errx.NewError(http.StatusConflict, "refund_not_disputed", "this refund is not with staff for a decision")

	// --- authorization ---
	ErrNotTheBuyer       = errx.NewError(http.StatusForbidden, "not_the_buyer", "only the buyer of this order may do that")
	ErrNotTheSeller      = errx.NewError(http.StatusForbidden, "not_the_seller", "only the seller of this order may do that")
	ErrModeratorRequired = errx.NewError(http.StatusForbidden, "moderator_required", "moderator role required")
	// Editing the carrier registry decides who this platform books parcels with, and the buyer pays
	// for every one of them — a moderator answers tickets, an admin changes what the platform buys.
	ErrAdminRequired = errx.NewError(http.StatusForbidden, "admin_required", "admin role required")
	// ErrQuoteSourceInvalid is a quote naming no source or more than one. The parcel has to be
	// one purchase: a variant to estimate, or the draft or agreed terms about to be checked out.
	ErrQuoteSourceInvalid = errx.NewError(http.StatusBadRequest, "quote_source_invalid", "name exactly one of a variant, a draft or an offer")
	// ErrCheckoutEmpty is a quote or a checkout with no lines in it.
	ErrCheckoutEmpty = errx.NewError(http.StatusBadRequest, "checkout_empty", "there is nothing here to ship")
	// ErrShippingQuoteInvalid is a courier that answered with something that cannot be charged.
	ErrShippingQuoteInvalid = errx.NewError(http.StatusBadGateway, "shipping_quote_invalid", "the carrier did not return a usable delivery price")
	ErrCarrierUnknown       = errx.NewError(http.StatusUnprocessableEntity, "carrier_unknown", "no enabled transport option by that id")

	// errQuantityPositive is the shape rule the CHECK constraints also hold: a line for
	// zero of something is not a line.
	errQuantityPositive = errx.NewError(http.StatusUnprocessableEntity, "quantity_positive", "quantity must be at least one")
)
