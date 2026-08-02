package domain

import (
	"net/http"

	"shopnexus/internal/shared/errx"
)

// Account errors — every 4xx this module can produce. Not-found lives here so the
// postgres adapter can return it without importing the module root; the rest are
// business rules the service enforces.
var (
	// --- account and sign-in ---
	ErrAccountNotFound = errx.NewError(http.StatusNotFound, "account_not_found", "account not found")
	// One error for all three identifiers on purpose: telling a caller *which* one
	// collided turns registration into a way to ask "is this address registered".
	ErrIdentifierTaken = errx.NewError(http.StatusConflict, "identifier_taken", "email or phone or username already taken")
	// ErrNoIdentifier covers both directions: a new account with nothing to be addressed
	// by, and a PATCH that would clear the last one. Validate sees the resulting account
	// rather than the request, so there is one rule and one code.
	ErrNoIdentifier       = errx.NewError(http.StatusUnprocessableEntity, "no_identifier", "an account needs at least one of email, phone or username")
	ErrInvalidCredentials = errx.NewError(http.StatusUnauthorized, "invalid_credentials", "wrong credentials")
	ErrAccountSuspended   = errx.NewError(http.StatusForbidden, "account_suspended", "this account is suspended")
	ErrNoPassword         = errx.NewError(http.StatusUnprocessableEntity, "no_password", "this account signs in through a provider and has no password")
	ErrForbidden          = errx.NewError(http.StatusForbidden, "forbidden", "not allowed to act on this resource")
	ErrModeratorRequired  = errx.NewError(http.StatusForbidden, "moderator_required", "moderator role required")
	ErrAdminRequired      = errx.NewError(http.StatusForbidden, "admin_required", "admin role required")
	// ErrVersionConflict is a save built on a stale read: somebody else changed the
	// account between the load and the write, so the rule the root checked may no longer
	// hold. 409 because retrying the whole command is exactly the right response, and the
	// client is the one that knows whether it still wants to.
	ErrVersionConflict = errx.NewError(http.StatusConflict, "version_conflict", "this account changed while you were editing it; try again")

	// --- one-time secrets ---
	// Unknown, already used and expired are one error each time: they are the same
	// fact to a caller, and separating them leaks whether a guess ever existed.
	ErrInvalidResetToken        = errx.NewError(http.StatusUnauthorized, "invalid_reset_token", "reset token is unknown or already used or expired")
	ErrInvalidVerificationToken = errx.NewError(http.StatusUnauthorized, "invalid_verification_token", "verification token is unknown or already used or expired")
	ErrInvalidPhoneCode         = errx.NewError(http.StatusUnauthorized, "invalid_phone_code", "code is wrong, expired, or already used")
	ErrTooManyRequests          = errx.NewError(http.StatusTooManyRequests, "too_many_requests", "a message was already sent recently; try again later")

	// --- email ---
	ErrEmailAlreadyVerified = errx.NewError(http.StatusConflict, "email_already_verified", "this email is already verified")
	ErrNoEmail              = errx.NewError(http.StatusUnprocessableEntity, "no_email", "this account has no email")

	// --- federated identities ---
	ErrOAuthIdentityNotFound = errx.NewError(http.StatusNotFound, "oauth_identity_not_found", "no such linked provider")
	ErrLastSignInMethod      = errx.NewError(http.StatusUnprocessableEntity, "last_sign_in_method", "this is the only way left to sign in")

	// --- contacts ---
	ErrContactNotFound             = errx.NewError(http.StatusNotFound, "contact_not_found", "contact not found")
	ErrContactPhoneAlreadyVerified = errx.NewError(http.StatusConflict, "contact_phone_already_verified", "this phone is already verified")

	// --- devices ---
	ErrDeviceNotFound = errx.NewError(http.StatusNotFound, "device_not_found", "device not found")

	// --- follows ---
	ErrSelfFollow = errx.NewError(http.StatusUnprocessableEntity, "self_follow", "an account cannot follow itself")

	// --- identity documents ---
	ErrIdentityDocumentNotFound = errx.NewError(http.StatusNotFound, "identity_document_not_found", "identity document not found")
	ErrIdentityAlreadyVerified  = errx.NewError(http.StatusConflict, "identity_already_verified", "this account already holds a live verified document")
	ErrIdentityAlreadyDecided   = errx.NewError(http.StatusConflict, "identity_already_decided", "this document already has a verdict")
	ErrIdentityExpiryRequired   = errx.NewError(http.StatusBadRequest, "identity_expiry_required", "this document type expires, so expires_at is required")
	// ErrScanUnavailable is a scan the vendor cannot be given: the resource does not
	// exist, belongs to nobody, or its upload was never confirmed. 422 rather than 404,
	// because the request named something real to *us* and the fixable part is the upload.
	ErrScanUnavailable         = errx.NewError(http.StatusUnprocessableEntity, "scan_unavailable", "a scan for this verification is missing or not readable")
	ErrRejectionReasonRequired = errx.NewError(http.StatusBadRequest, "rejection_reason_required", "a rejection needs a reason")
	// ErrIdentityVendorIncomplete is a vendor answer with no provider or no case
	// reference. 502: the request was fine and the dependency did not hold up its end.
	ErrIdentityVendorIncomplete = errx.NewError(http.StatusBadGateway, "identity_vendor_incomplete", "the verification vendor returned no case reference")
	// ErrNoPickupContact is a seller who has never set a collection point. It stops a sale
	// rather than guessing an address: a parcel collected from the wrong place is worse
	// than a checkout that says why it cannot proceed.
	ErrNoPickupContact = errx.NewError(http.StatusUnprocessableEntity, "no_pickup_contact", "the seller has no pickup address on file")
	// ErrNoDeliveryContact is a buyer with no default delivery address. A quote cannot be made
	// without one, and the client's answer is to ask for an address rather than to show a zero.
	ErrNoDeliveryContact = errx.NewError(http.StatusUnprocessableEntity, "no_delivery_contact", "you have no default delivery address on file")
)
