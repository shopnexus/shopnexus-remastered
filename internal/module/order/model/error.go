package ordermodel

import (
	"net/http"
	"shopnexus-server/internal/shared/errors"
)

// Sentinel errors for the order module.
var (
	ErrOrderItemNotFound = errors.NewError(
		http.StatusNotFound,
		"order_item_not_found",
		"Sorry, we couldn't find the item you requested",
	)
	ErrPaymentGatewayNotFound = errors.NewError(
		http.StatusNotFound,
		"payment_gateway_not_found",
		"Sorry, we couldn't find the payment gateway you requested",
	)
	ErrRefundCannotBeUpdated = errors.NewError(
		http.StatusConflict,
		"refund_cannot_be_updated",
		"Refund cannot be updated in its current status",
	)
	ErrBuyNowSingleSkuOnly = errors.NewError(
		http.StatusBadRequest,
		"buy_now_single_sku_only",
		"Buy now is only available for a single product",
	)
	ErrOrderNotFound         = errors.NewError(http.StatusNotFound, "order_not_found", "The order could not be found")
	ErrQuantityParamRequired = errors.NewError(
		http.StatusBadRequest,
		"quantity_param_required",
		"Either quantity or delta_quantity must be provided",
	)
	ErrBuyNowQuantityRequired = errors.NewError(http.StatusBadRequest, "buy_now_quantity_required", "Quantity is required for buy now checkout")
	ErrSkuNotFoundInCart      = errors.NewError(http.StatusNotFound, "sku_not_found_in_cart", "Some SKU not found in cart")
	ErrPaymentCannotCancel    = errors.NewError(http.StatusConflict, "payment_cannot_cancel", "Payment cannot be canceled")
	ErrOrderCannotCancel      = errors.NewError(http.StatusConflict, "order_cannot_cancel", "Order cannot be canceled")
	ErrOrderNotConfirmable    = errors.NewError(http.StatusConflict, "order_not_confirmable", "Order is not in a confirmable state")
	ErrMissingPayment         = errors.NewError(http.StatusNotFound, "missing_payment", "Payment record not found for order")
	ErrMissingPromotedPrice   = errors.NewError(http.StatusNotFound, "missing_promoted_price", "Promoted price not found for SKU")

	ErrItemsNotSameBuyer      = errors.NewError(http.StatusBadRequest, "items_not_same_buyer", "all items must belong to the same buyer")
	ErrItemsNotSameAddress    = errors.NewError(http.StatusBadRequest, "items_not_same_address", "all items must have the same address")
	ErrItemNotPending         = errors.NewError(http.StatusBadRequest, "item_not_pending", "item is not in pending status")
	ErrItemNotOwnedBySeller   = errors.NewError(http.StatusForbidden, "item_not_owned_by_seller", "item does not belong to this seller")
	ErrOrderNotPayable        = errors.NewError(http.StatusBadRequest, "order_not_payable", "order is not payable")
	ErrOrderAlreadyPaid       = errors.NewError(http.StatusBadRequest, "order_already_paid", "order is already paid")
	ErrUnknownTransportOption = errors.NewError(http.StatusBadRequest, "unknown_transport_option", "unknown transport option")
	ErrNoDefaultPaymentMethod = errors.NewError(http.StatusBadRequest, "no_default_payment_method", "no default payment method configured")
	ErrPaymentMethodNotFound  = errors.NewError(http.StatusNotFound, "payment_method_not_found", "payment method not found")

	ErrDisputeNotFound       = errors.NewError(http.StatusNotFound, "dispute_not_found", "dispute not found")
	ErrDisputeRefundResolved = errors.NewError(
		http.StatusConflict,
		"dispute_refund_resolved",
		"cannot dispute a refund that has already been resolved or cancelled",
	)
	ErrDisputeAlreadyActive = errors.NewError(
		http.StatusConflict,
		"dispute_already_active",
		"an active dispute already exists for this refund",
	)
	ErrDisputeNotAuthorized = errors.NewError(
		http.StatusForbidden,
		"dispute_not_authorized",
		"you are not authorized to access this dispute",
	)

	ErrPaymentNotSuccess = errors.NewError(
		http.StatusBadRequest,
		"payment_not_success",
		"payment has not been completed successfully",
	)
	ErrPaymentExpired = errors.NewError(
		http.StatusConflict,
		"payment_expired",
		"payment session has expired",
	)
	ErrItemAlreadyCancelled   = errors.NewError(http.StatusConflict, "item_already_cancelled", "item already cancelled")
	ErrItemAlreadyConfirmed   = errors.NewError(http.StatusConflict, "item_already_confirmed", "item already confirmed in an order")
	ErrItemsTransportMismatch = errors.NewError(http.StatusBadRequest, "items_transport_mismatch", "all items must have the same transport option")
	ErrPaymentTimeout         = errors.NewError(http.StatusConflict, "payment_timeout", "payment session expired")
	ErrPaymentFailed          = errors.NewError(http.StatusPaymentRequired, "payment_failed", "payment failed")
	ErrSellerConfirmTimeout   = errors.NewError(http.StatusConflict, "seller_confirm_timeout", "seller confirmation expired")
	ErrCheckoutCancelled      = errors.NewError(http.StatusConflict, "checkout_cancelled", "checkout cancelled by buyer")
	ErrCheckoutExpired        = errors.NewError(http.StatusConflict, "checkout_expired", "checkout session expired")
	ErrConfirmCancelled       = errors.NewError(http.StatusConflict, "confirm_cancelled", "confirmation cancelled by buyer")
	ErrConfirmExpired         = errors.NewError(http.StatusConflict, "confirm_expired", "confirmation session expired")

	ErrUnknownPaymentOption = errors.NewErrorf(http.StatusBadRequest, "unknown_payment_option", "Unknown payment option: %s")

	ErrCheckoutAddressCountryMismatch = errors.NewErrorf(
		http.StatusBadRequest,
		"address_country_mismatch",
		"address resolves to %s, buyer country is %s",
	)
	ErrFXRateUnavailable = errors.NewErrorf(
		http.StatusServiceUnavailable,
		"fx_rate_unavailable",
		"fx rate unavailable for %s",
	)
	ErrTransportStatusInvalid = errors.NewErrorf(
		http.StatusConflict,
		"transport_status_invalid",
		"cannot transition transport from %s to %s",
	)

	// Transaction ledger errors
	ErrTxNotFound                = errors.NewError(http.StatusNotFound, "tx_not_found", "transaction not found")
	ErrTxAlreadyFinal            = errors.NewError(http.StatusConflict, "tx_already_final", "transaction is already in a terminal state")
	ErrInsufficientWalletBalance = errors.NewError(http.StatusPaymentRequired, "wallet_insufficient", "internal wallet balance insufficient and no gateway fallback specified")

	// Refund v2 errors
	ErrRefundAlreadyAccepted  = errors.NewError(http.StatusConflict, "refund_already_accepted", "an active refund already exists for this order")
	ErrRefundAlreadyFinal     = errors.NewError(http.StatusConflict, "refund_already_final", "refund is already in a terminal state")
	ErrRefundWrongStage       = errors.NewError(http.StatusConflict, "refund_wrong_stage", "refund is not in the expected stage for this action")
	ErrRefundOrderNotPaid     = errors.NewError(http.StatusConflict, "refund_order_not_paid", "cannot refund an order that has not been paid")
	ErrRefundEvidenceRequired = errors.NewError(http.StatusBadRequest, "refund_evidence_required", "at least one evidence photo is required")
	ErrRefundNotWithdrawable  = errors.NewError(http.StatusConflict, "refund_not_withdrawable", "refund can only be withdrawn by its buyer while still in Shipping")

	// Dispute errors
	ErrUnauthorized  = errors.NewError(http.StatusForbidden, "unauthorized", "account is not permitted to perform this operation")
	ErrAdminRequired = errors.NewError(http.StatusForbidden, "admin_required", "only platform staff can resolve refund disputes")

	// Payout guard
	ErrOrderHasActiveRefund = errors.NewError(http.StatusConflict, "has_active_refund", "cannot release escrow; an active refund exists for this order")

	// Item domain errors
	ErrItemNotOwnedByBuyer = errors.NewError(http.StatusForbidden, "item_not_owned_by_buyer", "item is not owned by this buyer")
	ErrItemNotConfirmed    = errors.NewError(http.StatusConflict, "item_not_confirmed", "item has not been confirmed into an order")
)
