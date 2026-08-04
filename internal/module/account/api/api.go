// Package accountapi is the published contract of the account service: sign-in,
// the caller's own account and profile, saved addresses, push devices, the
// notification feed and its preferences, the follow graph, payout identity
// verification, and the moderator surface over all of it.
//
// One account, both roles: the same account buys and sells, its profile *is* the
// shop page, and Role only separates a user from the staff who moderate them.
//
// Only interfaces, DTOs and validate tags live here. Every key field is an
// id.ID[K], which marshals to the opaque wire form; a raw int64 never leaves the
// module.
package accountapi

import (
	"context"

	"shopnexus/internal/module/common"
)

// PageInfo is where a page sits in a page-paginated collection. TotalCount is a
// pointer because null is a real answer for a result the query never counted.
type PageInfo struct {
	Page       int
	Limit      int
	TotalCount *int64
}

// Page is one page of a browsable collection, and Cursor one page of an
// append-only stream. Which a collection uses follows from the collection: the
// notification feed is chunked by time and is only ever read newest-first, so it
// seeks by cursor; a follower list is browsed with a pager.
type Page[T any] struct {
	Data []T
	Meta PageInfo
}

type Cursor[T any] struct {
	Data []T
	// NextCursor is nil on the last page, so "no more pages" is a value rather than
	// a missing one.
	NextCursor *string
}

// Service is the account module's contract. Every method that acts on behalf of a
// signed-in caller takes their id in the request's ActorID, which the gateway
// fills from the token — never from the body.
//
// The role checks behind the admin methods are enforced here rather than in the
// handler: the caller's role is a fact about a row in this module's table, so the
// handler would have to ask this service for it anyway.
type Service interface {
	// --- authentication ---
	Register(ctx context.Context, req RegisterRequest) (AuthResult, error)
	Login(ctx context.Context, req LoginRequest) (AuthResult, error)
	LoginOAuth(ctx context.Context, req OAuthLoginRequest) (OAuthLoginResult, error)
	Refresh(ctx context.Context, req RefreshRequest) (AuthResult, error)
	Logout(ctx context.Context, req LogoutRequest) error
	ChangePassword(ctx context.Context, req ChangePasswordRequest) error
	RequestPasswordReset(ctx context.Context, req PasswordResetRequest) error
	ResetPassword(ctx context.Context, req PasswordResetConfirmRequest) error
	RequestEmailVerification(ctx context.Context, req RequestEmailVerificationRequest) error
	VerifyEmail(ctx context.Context, req EmailVerificationRequest) error

	// --- the caller and other accounts ---
	GetMe(ctx context.Context, req GetMeRequest) (Me, error)
	UpdateMe(ctx context.Context, req UpdateAccountRequest) (Me, error)
	UpdateProfile(ctx context.Context, req UpdateProfileRequest) (Profile, error)
	GetPublicAccount(ctx context.Context, req GetPublicAccountRequest) (PublicAccount, error)
	ListOAuthIdentities(ctx context.Context, req ListOAuthIdentitiesRequest) ([]OAuthIdentity, error)
	UnlinkOAuthIdentity(ctx context.Context, req UnlinkOAuthIdentityRequest) error

	// CreateUpload reserves a row and a presigned slot for an avatar or an identity scan;
	// ConfirmUpload makes it real once the bytes are at the store. Until then the resource
	// resolves to nothing, so a half-finished upload cannot be attached to anything.
	CreateUpload(ctx context.Context, req CreateUploadRequest) (UploadSlot, error)
	ConfirmUpload(ctx context.Context, req ConfirmUploadRequest) (common.ResourceDTO, error)

	// --- saved addresses ---
	ListContacts(ctx context.Context, req ListContactsRequest) ([]Contact, error)
	GetContact(ctx context.Context, req GetContactRequest) (Contact, error)
	// GetPickupContact answers a seller's collection point, which the order module needs to
	// create a shipment while the seller is not present. Pickup only.
	GetPickupContact(ctx context.Context, req GetPickupContactRequest) (Contact, error)
	// GetSupportAccount answers the support desk's own account: the second side of every ticket
	// thread. Its own method rather than a config value, because an id in configuration is one a
	// deployment can get wrong in a way nothing checks.
	GetSupportAccount(ctx context.Context) (AccountSummary, error)
	// GetDeliveryContact answers the caller's default delivery address, so a quote can be made
	// without asking them to pick one first.
	GetDeliveryContact(ctx context.Context, req GetDeliveryContactRequest) (Contact, error)
	CreateContact(ctx context.Context, req CreateContactRequest) (Contact, error)
	UpdateContact(ctx context.Context, req UpdateContactRequest) (Contact, error)
	DeleteContact(ctx context.Context, req DeleteContactRequest) error
	RequestContactPhoneVerification(ctx context.Context, req RequestContactPhoneVerificationRequest) error
	VerifyContactPhone(ctx context.Context, req VerifyContactPhoneRequest) (Contact, error)

	// --- push devices ---
	RegisterDevice(ctx context.Context, req RegisterDeviceRequest) (Device, error)
	ListDevices(ctx context.Context, req ListDevicesRequest) ([]Device, error)
	DeleteDevice(ctx context.Context, req DeleteDeviceRequest) error

	// --- notifications ---
	ListNotifications(ctx context.Context, req ListNotificationsRequest) (Cursor[Notification], error)
	GetUnreadCount(ctx context.Context, req GetUnreadCountRequest) (UnreadCount, error)
	MarkNotificationsRead(ctx context.Context, req MarkNotificationsReadRequest) (UnreadCount, error)
	GetNotificationPreferences(ctx context.Context, req GetNotificationPreferencesRequest) ([]NotificationPreference, error)
	UpdateNotificationPreferences(ctx context.Context, req UpdateNotificationPreferencesRequest) ([]NotificationPreference, error)
	CreateNotification(ctx context.Context, req CreateNotificationRequest) (Notification, error)

	// --- follow graph ---
	ListFollowing(ctx context.Context, req ListFollowingRequest) (Page[AccountSummary], error)
	ListFollowers(ctx context.Context, req ListFollowersRequest) (Page[AccountSummary], error)
	Follow(ctx context.Context, req FollowRequest) error
	Unfollow(ctx context.Context, req UnfollowRequest) error

	// --- payout identity verification ---
	StartIdentityVerification(ctx context.Context, req StartIdentityVerificationRequest) (IdentityVerificationTicket, error)
	ListIdentityDocuments(ctx context.Context, req ListIdentityDocumentsRequest) ([]IdentityDocument, error)

	// --- moderator and admin ---
	AdminListAccounts(ctx context.Context, req AdminListAccountsRequest) (Page[AdminAccount], error)
	AdminSuspendAccount(ctx context.Context, req SuspendAccountRequest) (AdminAccount, error)
	AdminLiftSuspension(ctx context.Context, req LiftSuspensionRequest) (AdminAccount, error)
	AdminCreateModerator(ctx context.Context, req CreateModeratorRequest) (AdminAccount, error)
	AdminRevokeModerator(ctx context.Context, req RevokeModeratorRequest) error
	AdminListIdentityDocuments(ctx context.Context, req AdminListIdentityDocumentsRequest) (Page[AdminIdentityDocument], error)
	AdminRecordIdentityVerdict(ctx context.Context, req IdentityVerdictRequest) (IdentityDocument, error)
}
