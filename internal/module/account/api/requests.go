package accountapi

import (
	"time"

	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/patch"
)

// A field tagged `json:"-"` is filled by the gateway from the token or the path,
// never from the body: a request that could name its own actor is a request that
// can act as somebody else.
//
// Optional fields of a PATCH body are patch.Field, which distinguishes "absent"
// from "null" — the API needs both, since sending null is how an identifier or an
// avatar is removed.

// --- authentication ---

// RegisterRequest always creates a plain user; moderators come from
// POST /admin/moderators. At least one identifier is required, which the domain
// enforces because the same rule holds for every other way a row is created.
type RegisterRequest struct {
	Email    string `json:"email,omitempty" validate:"omitempty,email,max=255"`
	Phone    string `json:"phone,omitempty" validate:"omitempty,e164"`
	Username string `json:"username,omitempty" validate:"omitempty,min=3,max=100"`
	Password string `json:"password" validate:"required,min=8,max=72"`
	Name     string `json:"name" validate:"required,min=1,max=100"`
	Country  string `json:"country" validate:"required,len=2"`
	Locale   string `json:"locale" validate:"required,max=10"`
	Timezone string `json:"timezone" validate:"required,max=64"`
}

// LoginRequest's Identifier is an email or a phone or a username. Which one it is
// does not change the answer and is not reported back.
type LoginRequest struct {
	Identifier string `json:"identifier" validate:"required,max=255"`
	Password   string `json:"password" validate:"required"`
}

// OAuthLoginRequest carries the provider's own credential — an authorization code
// or an id token. The profile fields are used only when the sign-in creates an
// account; a provider that returns nothing usable falls back to defaults.
type OAuthLoginRequest struct {
	Provider   string `json:"provider" validate:"required,max=30"`
	Credential string `json:"credential" validate:"required"`
	Country    string `json:"country,omitempty" validate:"omitempty,len=2"`
	Locale     string `json:"locale,omitempty" validate:"omitempty,max=10"`
	Timezone   string `json:"timezone,omitempty" validate:"omitempty,max=64"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" validate:"required"`
}

// LogoutRequest revokes the caller's current session, and unregisters the device it
// names so the phone stops receiving notifications for an account nobody is signed
// in to.
type LogoutRequest struct {
	ActorID   id.ID[id.Account] `json:"-" validate:"required"`
	SessionID string            `json:"-" validate:"required"`
	DeviceID  id.ID[id.Device]  `json:"device_id,omitempty"`
}

// ChangePasswordRequest requires the current password even though the caller is
// already authenticated: a stolen access token must not be enough to take the
// account. The caller's own session survives; every other one is dropped.
type ChangePasswordRequest struct {
	ActorID         id.ID[id.Account] `json:"-" validate:"required"`
	SessionID       string            `json:"-" validate:"required"`
	CurrentPassword string            `json:"current_password" validate:"required"`
	NewPassword     string            `json:"new_password" validate:"required,min=8,max=72"`
}

// PasswordResetRequest's Identifier is an email or a phone.
type PasswordResetRequest struct {
	Identifier string `json:"identifier" validate:"required,max=255"`
}

type PasswordResetConfirmRequest struct {
	Token       string `json:"token" validate:"required"`
	NewPassword string `json:"new_password" validate:"required,min=8,max=72"`
}

type RequestEmailVerificationRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
}

type EmailVerificationRequest struct {
	Token string `json:"token" validate:"required"`
}

// --- the caller and other accounts ---

type GetMeRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
}

// UpdateAccountRequest changes the caller's identifiers. Every field is optional;
// null removes one, which is refused when it is the last.
type UpdateAccountRequest struct {
	ActorID  id.ID[id.Account]   `json:"-" validate:"required"`
	Email    patch.Field[string] `json:"email"`
	Phone    patch.Field[string] `json:"phone"`
	Username patch.Field[string] `json:"username"`
}

// UpdateProfileRequest is the shop front. Locale and timezone also decide how
// notifications are written and when they are sent.
type UpdateProfileRequest struct {
	ActorID          id.ID[id.Account]               `json:"-" validate:"required"`
	Name             patch.Field[string]             `json:"name"`
	Description      patch.Field[string]             `json:"description"`
	Gender           patch.Field[string]             `json:"gender"`
	DateOfBirth      patch.Field[string]             `json:"date_of_birth"`
	AvatarResourceID patch.Field[id.ID[id.Resource]] `json:"avatar_resource_id"`
	Country          patch.Field[string]             `json:"country"`
	Locale           patch.Field[string]             `json:"locale"`
	Timezone         patch.Field[string]             `json:"timezone"`
}

type GetPublicAccountRequest struct {
	ID id.ID[id.Account] `json:"-" validate:"required"`
}

type ListOAuthIdentitiesRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
}

type UnlinkOAuthIdentityRequest struct {
	ActorID  id.ID[id.Account] `json:"-" validate:"required"`
	Provider string            `json:"-" validate:"required,max=30"`
}

// --- saved addresses ---

type ListContactsRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
}

// CreateContactRequest's administrative codes are what a carrier is called with, so
// they are required; the coordinate is not, because geocoding may fail and the
// address still has to be saveable.
type CreateContactRequest struct {
	ActorID           id.ID[id.Account] `json:"-" validate:"required"`
	FullName          string            `json:"full_name" validate:"required,min=1,max=100"`
	Phone             string            `json:"phone" validate:"required,e164"`
	AddressType       string            `json:"address_type" validate:"required,oneof=home work"`
	IsDefaultDelivery bool              `json:"is_default_delivery"`
	IsDefaultPickup   bool              `json:"is_default_pickup"`
	Country           string            `json:"country" validate:"required,len=2"`
	ProvinceCode      string            `json:"province_code" validate:"required,max=20"`
	ProvinceName      string            `json:"province_name" validate:"required,max=100"`
	DistrictCode      string            `json:"district_code,omitempty" validate:"max=20"`
	DistrictName      string            `json:"district_name,omitempty" validate:"max=100"`
	WardCode          string            `json:"ward_code" validate:"required,max=20"`
	WardName          string            `json:"ward_name" validate:"required,max=100"`
	PostalCode        string            `json:"postal_code,omitempty" validate:"max=20"`
	Address           string            `json:"address" validate:"required,min=1,max=255"`
	AddressDetail     string            `json:"address_detail,omitempty" validate:"max=255"`
	Latitude          *float64          `json:"latitude,omitempty" validate:"omitempty,gte=-90,lte=90"`
	Longitude         *float64          `json:"longitude,omitempty" validate:"omitempty,gte=-180,lte=180"`
}

// UpdateContactRequest: every field optional. Changing the phone clears
// phone_verified, and setting a default clears the previous one. An order already
// placed keeps its own snapshot, so editing this row never rewrites where a
// shipment was going.
type UpdateContactRequest struct {
	ActorID           id.ID[id.Account]    `json:"-" validate:"required"`
	ID                id.ID[id.Contact]    `json:"-" validate:"required"`
	FullName          patch.Field[string]  `json:"full_name"`
	Phone             patch.Field[string]  `json:"phone"`
	AddressType       patch.Field[string]  `json:"address_type"`
	IsDefaultDelivery patch.Field[bool]    `json:"is_default_delivery"`
	IsDefaultPickup   patch.Field[bool]    `json:"is_default_pickup"`
	ProvinceCode      patch.Field[string]  `json:"province_code"`
	ProvinceName      patch.Field[string]  `json:"province_name"`
	DistrictCode      patch.Field[string]  `json:"district_code"`
	DistrictName      patch.Field[string]  `json:"district_name"`
	WardCode          patch.Field[string]  `json:"ward_code"`
	WardName          patch.Field[string]  `json:"ward_name"`
	PostalCode        patch.Field[string]  `json:"postal_code"`
	Address           patch.Field[string]  `json:"address"`
	AddressDetail     patch.Field[string]  `json:"address_detail"`
	Latitude          patch.Field[float64] `json:"latitude"`
	Longitude         patch.Field[float64] `json:"longitude"`
}

type DeleteContactRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	ID      id.ID[id.Contact] `json:"-" validate:"required"`
}

type RequestContactPhoneVerificationRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	ID      id.ID[id.Contact] `json:"-" validate:"required"`
}

type VerifyContactPhoneRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	ID      id.ID[id.Contact] `json:"-" validate:"required"`
	Code    string            `json:"code" validate:"required,min=4,max=10"`
}

// --- push devices ---

// RegisterDeviceRequest is an upsert on the push token, not a create: the token
// identifies an install and the platform reissues the same one when a different
// user signs in on that phone, so the row moves to the new account.
type RegisterDeviceRequest struct {
	ActorID   id.ID[id.Account] `json:"-" validate:"required"`
	Platform  string            `json:"platform" validate:"required,oneof=ios android web"`
	PushToken string            `json:"push_token" validate:"required,min=1,max=4096"`
}

type ListDevicesRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
}

type DeleteDeviceRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	ID      id.ID[id.Device]  `json:"-" validate:"required"`
}

// --- notifications ---

type ListNotificationsRequest struct {
	ActorID  id.ID[id.Account] `json:"-" validate:"required"`
	Category string            `json:"-" validate:"omitempty,oneof=order promotion system chat social"`
	// Unread is a pointer because "unread=false" is a filter and "no unread
	// parameter" is not.
	Unread *bool  `json:"-"`
	Cursor string `json:"-"`
	Limit  int    `json:"-" validate:"required,min=1,max=100"`
}

type GetUnreadCountRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
}

// MarkNotificationsReadRequest takes a time bound rather than a list of ids:
// marking individual rows would search every chunk of a time-partitioned table,
// while a bound reads one range. Omit Before to mark the whole feed read.
type MarkNotificationsReadRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	Before  *time.Time        `json:"before,omitempty"`
}

type GetNotificationPreferencesRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
}

// PreferenceInput is one requested pair. Setting a pair back to its default deletes
// the stored row rather than storing the default again.
type PreferenceInput struct {
	Category  string `json:"category" validate:"required,oneof=order promotion system chat social"`
	Channel   string `json:"channel" validate:"required,oneof=in-app push email sms"`
	IsEnabled *bool  `json:"is_enabled" validate:"required"`
}

type UpdateNotificationPreferencesRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	Items   []PreferenceInput `json:"items" validate:"required,min=1,dive"`
}

// --- follow graph ---

type ListFollowingRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	Page    int               `json:"-" validate:"required,min=1"`
	Limit   int               `json:"-" validate:"required,min=1,max=100"`
}

type ListFollowersRequest struct {
	AccountID id.ID[id.Account] `json:"-" validate:"required"`
	Page      int               `json:"-" validate:"required,min=1"`
	Limit     int               `json:"-" validate:"required,min=1,max=100"`
}

type FollowRequest struct {
	ActorID  id.ID[id.Account] `json:"-" validate:"required"`
	TargetID id.ID[id.Account] `json:"-" validate:"required"`
}

type UnfollowRequest struct {
	ActorID  id.ID[id.Account] `json:"-" validate:"required"`
	TargetID id.ID[id.Account] `json:"-" validate:"required"`
}

// --- payout identity verification ---

// StartIdentityVerificationRequest names the scans the vendor reads. They are uploaded
// first through POST /resources and referenced by id here: a photo of a government ID is
// not something to push through a JSON body, and the upload has its own retry story.
//
// The back is required only where the document has one, and the selfie is what a face
// match compares against the portrait — without it there is nothing tying the document
// to the person holding the account.
type StartIdentityVerificationRequest struct {
	ActorID          id.ID[id.Account]  `json:"-" validate:"required"`
	DocType          string             `json:"doc_type" validate:"required,oneof=national-id passport driver-license"`
	FrontResourceID  id.ID[id.Resource] `json:"front_resource_id" validate:"required"`
	BackResourceID   id.ID[id.Resource] `json:"back_resource_id,omitempty"`
	SelfieResourceID id.ID[id.Resource] `json:"selfie_resource_id" validate:"required"`
}

type ListIdentityDocumentsRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
}

// --- moderator and admin ---

// AdminListAccountsRequest's Query is an exact match on email, phone or username —
// each is unique, so that is a key lookup — or a fragment match on the display
// name, answered by a trigram index. It is one query either way; the caller does
// not choose.
type AdminListAccountsRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	Query   string            `json:"-" validate:"max=255"`
	Status  string            `json:"-" validate:"omitempty,oneof=active suspended"`
	Role    string            `json:"-" validate:"omitempty,oneof=user moderator admin"`
	Page    int               `json:"-" validate:"required,min=1"`
	Limit   int               `json:"-" validate:"required,min=1,max=100"`
}

// SuspendAccountRequest: omit Until for a permanent suspension. Suspending also
// drops the account's sessions, since a suspended row does not stop an access token
// already in circulation.
type SuspendAccountRequest struct {
	ActorID   id.ID[id.Account] `json:"-" validate:"required"`
	AccountID id.ID[id.Account] `json:"-" validate:"required"`
	Reason    string            `json:"reason" validate:"required,min=1,max=2000"`
	Until     *time.Time        `json:"until,omitempty"`
}

type LiftSuspensionRequest struct {
	ActorID   id.ID[id.Account] `json:"-" validate:"required"`
	AccountID id.ID[id.Account] `json:"-" validate:"required"`
}

// CreateModeratorRequest is admin-only and has no self-service path: a moderator
// decides disputes and takes listings down, so the role is granted, never claimed.
type CreateModeratorRequest struct {
	ActorID  id.ID[id.Account] `json:"-" validate:"required"`
	Email    string            `json:"email" validate:"required,email,max=255"`
	Password string            `json:"password" validate:"required,min=8,max=72"`
	Name     string            `json:"name" validate:"required,min=1,max=100"`
	Country  string            `json:"country" validate:"required,len=2"`
	Locale   string            `json:"locale" validate:"required,max=10"`
	Timezone string            `json:"timezone" validate:"required,max=64"`
}

type RevokeModeratorRequest struct {
	ActorID   id.ID[id.Account] `json:"-" validate:"required"`
	AccountID id.ID[id.Account] `json:"-" validate:"required"`
}

type AdminListIdentityDocumentsRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	Status  string            `json:"-" validate:"required,oneof=pending verified rejected"`
	Page    int               `json:"-" validate:"required,min=1"`
	Limit   int               `json:"-" validate:"required,min=1,max=100"`
}

// IdentityVerdictRequest records a decision, so 'pending' is not a choice.
// 'verified' requires ExpiresAt when the document type carries one, because the
// payout gate reads the expiry and not only the status; 'rejected' requires a
// reason.
type IdentityVerdictRequest struct {
	ActorID         id.ID[id.Account]          `json:"-" validate:"required"`
	DocumentID      id.ID[id.IdentityDocument] `json:"-" validate:"required"`
	Status          string                     `json:"status" validate:"required,oneof=verified rejected"`
	RejectionReason string                     `json:"rejection_reason,omitempty" validate:"max=2000"`
	ExpiresAt       *time.Time                 `json:"expires_at,omitempty"`
}
