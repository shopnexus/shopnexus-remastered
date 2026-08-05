// Package accounttest provides a stub accountapi.Service for tests.
//
// The contract has forty-odd methods, and a test that cares about one of them should not
// have to write the other forty. Embed Stub and override what the test is about; anything
// left over answers 501, so an unstubbed call shows up as an obviously wrong status rather
// than as a plausible zero value.
package accounttest

import (
	"context"

	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/common"
	"shopnexus/internal/shared/errx"
)

// Stub implements accountapi.Service by refusing everything.
type Stub struct{}

var _ accountapi.Service = Stub{}

func (Stub) Register(context.Context, accountapi.RegisterRequest) (accountapi.AuthResult, error) {
	return accountapi.AuthResult{}, errx.ErrNotImplemented
}

func (Stub) Login(context.Context, accountapi.LoginRequest) (accountapi.AuthResult, error) {
	return accountapi.AuthResult{}, errx.ErrNotImplemented
}

func (Stub) LoginOAuth(context.Context, accountapi.OAuthLoginRequest) (accountapi.OAuthLoginResult, error) {
	return accountapi.OAuthLoginResult{}, errx.ErrNotImplemented
}

func (Stub) Refresh(context.Context, accountapi.RefreshRequest) (accountapi.AuthResult, error) {
	return accountapi.AuthResult{}, errx.ErrNotImplemented
}

func (Stub) Logout(context.Context, accountapi.LogoutRequest) error { return errx.ErrNotImplemented }

func (Stub) ChangePassword(context.Context, accountapi.ChangePasswordRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) RequestPasswordReset(context.Context, accountapi.PasswordResetRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) ResetPassword(context.Context, accountapi.PasswordResetConfirmRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) RequestEmailVerification(context.Context, accountapi.RequestEmailVerificationRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) VerifyEmail(context.Context, accountapi.EmailVerificationRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) GetMe(context.Context, accountapi.GetMeRequest) (accountapi.Me, error) {
	return accountapi.Me{}, errx.ErrNotImplemented
}

func (Stub) UpdateMe(context.Context, accountapi.UpdateAccountRequest) (accountapi.Me, error) {
	return accountapi.Me{}, errx.ErrNotImplemented
}

func (Stub) UpdateProfile(context.Context, accountapi.UpdateProfileRequest) (accountapi.Profile, error) {
	return accountapi.Profile{}, errx.ErrNotImplemented
}

func (Stub) GetPublicAccount(context.Context, accountapi.GetPublicAccountRequest) (accountapi.PublicAccount, error) {
	return accountapi.PublicAccount{}, errx.ErrNotImplemented
}

func (Stub) ListOAuthIdentities(context.Context, accountapi.ListOAuthIdentitiesRequest) ([]accountapi.OAuthIdentity, error) {
	return nil, errx.ErrNotImplemented
}

func (Stub) UnlinkOAuthIdentity(context.Context, accountapi.UnlinkOAuthIdentityRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) CreateUpload(context.Context, accountapi.CreateUploadRequest) (accountapi.UploadSlot, error) {
	return accountapi.UploadSlot{}, errx.ErrNotImplemented
}

func (Stub) ConfirmUpload(context.Context, accountapi.ConfirmUploadRequest) (common.ResourceDTO, error) {
	return common.ResourceDTO{}, errx.ErrNotImplemented
}

func (Stub) ListAdministrativeAreas(context.Context, accountapi.ListAdministrativeAreasRequest) ([]accountapi.AdministrativeArea, error) {
	return nil, errx.ErrNotImplemented
}

func (Stub) ListContacts(context.Context, accountapi.ListContactsRequest) ([]accountapi.Contact, error) {
	return nil, errx.ErrNotImplemented
}

func (Stub) GetContact(context.Context, accountapi.GetContactRequest) (accountapi.Contact, error) {
	return accountapi.Contact{}, errx.ErrNotImplemented
}

func (Stub) GetPickupContact(context.Context, accountapi.GetPickupContactRequest) (accountapi.Contact, error) {
	return accountapi.Contact{}, errx.ErrNotImplemented
}

func (Stub) GetSupportAccount(context.Context) (accountapi.AccountSummary, error) {
	return accountapi.AccountSummary{}, errx.ErrNotImplemented
}

func (Stub) GetDeliveryContact(context.Context, accountapi.GetDeliveryContactRequest) (accountapi.Contact, error) {
	return accountapi.Contact{}, errx.ErrNotImplemented
}

func (Stub) CreateContact(context.Context, accountapi.CreateContactRequest) (accountapi.Contact, error) {
	return accountapi.Contact{}, errx.ErrNotImplemented
}

func (Stub) UpdateContact(context.Context, accountapi.UpdateContactRequest) (accountapi.Contact, error) {
	return accountapi.Contact{}, errx.ErrNotImplemented
}

func (Stub) DeleteContact(context.Context, accountapi.DeleteContactRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) RequestContactPhoneVerification(context.Context, accountapi.RequestContactPhoneVerificationRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) VerifyContactPhone(context.Context, accountapi.VerifyContactPhoneRequest) (accountapi.Contact, error) {
	return accountapi.Contact{}, errx.ErrNotImplemented
}

func (Stub) RegisterDevice(context.Context, accountapi.RegisterDeviceRequest) (accountapi.Device, error) {
	return accountapi.Device{}, errx.ErrNotImplemented
}

func (Stub) ListDevices(context.Context, accountapi.ListDevicesRequest) ([]accountapi.Device, error) {
	return nil, errx.ErrNotImplemented
}

func (Stub) DeleteDevice(context.Context, accountapi.DeleteDeviceRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) ListNotifications(context.Context, accountapi.ListNotificationsRequest) (accountapi.Cursor[accountapi.Notification], error) {
	return accountapi.Cursor[accountapi.Notification]{}, errx.ErrNotImplemented
}

func (Stub) GetUnreadCount(context.Context, accountapi.GetUnreadCountRequest) (accountapi.UnreadCount, error) {
	return accountapi.UnreadCount{}, errx.ErrNotImplemented
}

func (Stub) MarkNotificationsRead(context.Context, accountapi.MarkNotificationsReadRequest) (accountapi.UnreadCount, error) {
	return accountapi.UnreadCount{}, errx.ErrNotImplemented
}

func (Stub) GetNotificationPreferences(context.Context, accountapi.GetNotificationPreferencesRequest) ([]accountapi.NotificationPreference, error) {
	return nil, errx.ErrNotImplemented
}

func (Stub) UpdateNotificationPreferences(context.Context, accountapi.UpdateNotificationPreferencesRequest) ([]accountapi.NotificationPreference, error) {
	return nil, errx.ErrNotImplemented
}

func (Stub) CreateNotification(context.Context, accountapi.CreateNotificationRequest) (accountapi.Notification, error) {
	return accountapi.Notification{}, errx.ErrNotImplemented
}

func (Stub) ListFollowing(context.Context, accountapi.ListFollowingRequest) (accountapi.Page[accountapi.AccountSummary], error) {
	return accountapi.Page[accountapi.AccountSummary]{}, errx.ErrNotImplemented
}

func (Stub) ListFollowers(context.Context, accountapi.ListFollowersRequest) (accountapi.Page[accountapi.AccountSummary], error) {
	return accountapi.Page[accountapi.AccountSummary]{}, errx.ErrNotImplemented
}

func (Stub) Follow(context.Context, accountapi.FollowRequest) error { return errx.ErrNotImplemented }

func (Stub) Unfollow(context.Context, accountapi.UnfollowRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) StartIdentityVerification(context.Context, accountapi.StartIdentityVerificationRequest) (accountapi.IdentityVerificationTicket, error) {
	return accountapi.IdentityVerificationTicket{}, errx.ErrNotImplemented
}

func (Stub) ListIdentityDocuments(context.Context, accountapi.ListIdentityDocumentsRequest) ([]accountapi.IdentityDocument, error) {
	return nil, errx.ErrNotImplemented
}

func (Stub) AdminListAccounts(context.Context, accountapi.AdminListAccountsRequest) (accountapi.Page[accountapi.AdminAccount], error) {
	return accountapi.Page[accountapi.AdminAccount]{}, errx.ErrNotImplemented
}

func (Stub) AdminSuspendAccount(context.Context, accountapi.SuspendAccountRequest) (accountapi.AdminAccount, error) {
	return accountapi.AdminAccount{}, errx.ErrNotImplemented
}

func (Stub) AdminLiftSuspension(context.Context, accountapi.LiftSuspensionRequest) (accountapi.AdminAccount, error) {
	return accountapi.AdminAccount{}, errx.ErrNotImplemented
}

func (Stub) AdminCreateModerator(context.Context, accountapi.CreateModeratorRequest) (accountapi.AdminAccount, error) {
	return accountapi.AdminAccount{}, errx.ErrNotImplemented
}

func (Stub) AdminRevokeModerator(context.Context, accountapi.RevokeModeratorRequest) error {
	return errx.ErrNotImplemented
}

func (Stub) AdminListIdentityDocuments(context.Context, accountapi.AdminListIdentityDocumentsRequest) (accountapi.Page[accountapi.AdminIdentityDocument], error) {
	return accountapi.Page[accountapi.AdminIdentityDocument]{}, errx.ErrNotImplemented
}

func (Stub) AdminRecordIdentityVerdict(context.Context, accountapi.IdentityVerdictRequest) (accountapi.IdentityDocument, error) {
	return accountapi.IdentityDocument{}, errx.ErrNotImplemented
}
