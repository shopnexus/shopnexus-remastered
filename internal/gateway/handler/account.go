package handler

import (
	"log/slog"
	"net/http"

	"github.com/go-playground/validator/v10"

	accountapi "shopnexus/internal/module/account/api"
)

// Account serves the account module's routes: authentication, profile, contacts, devices, notifications, the social graph and identity verification.
//
// Scaffold. Every method answers 501 until it is written, and the routes are
// registered in router.go so the OpenAPI contract test can hold the two in step.
// The service, validator and logger are held already: it keeps the fx graph real —
// so the module's pool is opened and its config validated at startup — and makes
// filling a method in a local edit rather than a rewiring.
type Account struct {
	svc accountapi.Service
	v   *validator.Validate
	log *slog.Logger
}

func NewAccount(svc accountapi.Service, v *validator.Validate, log *slog.Logger) *Account {
	return &Account{svc: svc, v: v, log: log}
}

// Register handles POST /register.
func (h *Account) Register(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// Login handles POST /login.
func (h *Account) Login(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// LoginOAuth handles POST /login/oauth.
func (h *Account) LoginOAuth(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// RefreshToken handles POST /token/refresh.
func (h *Account) RefreshToken(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// Logout handles POST /logout.
func (h *Account) Logout(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// ChangePassword handles PUT /password.
func (h *Account) ChangePassword(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// RequestPasswordReset handles POST /password/reset-requests.
func (h *Account) RequestPasswordReset(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// ResetPassword handles POST /password/resets.
func (h *Account) ResetPassword(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// RequestEmailVerification handles POST /email/verification-requests.
func (h *Account) RequestEmailVerification(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// VerifyEmail handles POST /email/verifications.
func (h *Account) VerifyEmail(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// GetMe handles GET /me.
func (h *Account) GetMe(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// UpdateMe handles PATCH /me.
func (h *Account) UpdateMe(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// UpdateProfile handles PATCH /me/profile.
func (h *Account) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// GetPublicAccount handles GET /accounts/{id}.
func (h *Account) GetPublicAccount(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// ListOAuthIdentities handles GET /me/oauth-identities.
func (h *Account) ListOAuthIdentities(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// UnlinkOAuthIdentity handles DELETE /me/oauth-identities/{provider}.
func (h *Account) UnlinkOAuthIdentity(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// ListContacts handles GET /contacts.
func (h *Account) ListContacts(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// CreateContact handles POST /contacts.
func (h *Account) CreateContact(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// UpdateContact handles PATCH /contacts/{id}.
func (h *Account) UpdateContact(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// DeleteContact handles DELETE /contacts/{id}.
func (h *Account) DeleteContact(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// RegisterDevice handles PUT /devices.
func (h *Account) RegisterDevice(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// ListDevices handles GET /me/devices.
func (h *Account) ListDevices(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// DeleteDevice handles DELETE /devices/{id}.
func (h *Account) DeleteDevice(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// ListNotifications handles GET /notifications.
func (h *Account) ListNotifications(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// GetNotificationUnreadCount handles GET /notifications/unread-count.
func (h *Account) GetNotificationUnreadCount(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// MarkNotificationsRead handles POST /notifications/read.
func (h *Account) MarkNotificationsRead(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// GetNotificationPreferences handles GET /notification-preferences.
func (h *Account) GetNotificationPreferences(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// UpdateNotificationPreferences handles PUT /notification-preferences.
func (h *Account) UpdateNotificationPreferences(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// ListFavorites handles GET /favorites.
func (h *Account) ListFavorites(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AddFavorite handles PUT /favorites/{spuID}.
func (h *Account) AddFavorite(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// RemoveFavorite handles DELETE /favorites/{spuID}.
func (h *Account) RemoveFavorite(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// ListFollowing handles GET /me/following.
func (h *Account) ListFollowing(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// ListFollowers handles GET /accounts/{id}/followers.
func (h *Account) ListFollowers(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// Follow handles PUT /follows/{accountID}.
func (h *Account) Follow(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// Unfollow handles DELETE /follows/{accountID}.
func (h *Account) Unfollow(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// StartIdentityVerification handles POST /identity-documents.
func (h *Account) StartIdentityVerification(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// ListIdentityDocuments handles GET /me/identity-documents.
func (h *Account) ListIdentityDocuments(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AdminListAccounts handles GET /admin/accounts.
func (h *Account) AdminListAccounts(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AdminSuspendAccount handles POST /admin/accounts/{id}/suspension.
func (h *Account) AdminSuspendAccount(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AdminLiftSuspension handles DELETE /admin/accounts/{id}/suspension.
func (h *Account) AdminLiftSuspension(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AdminCreateModerator handles POST /admin/moderators.
func (h *Account) AdminCreateModerator(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AdminRevokeModerator handles DELETE /admin/moderators/{id}.
func (h *Account) AdminRevokeModerator(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AdminListIdentityDocuments handles GET /admin/identity-documents.
func (h *Account) AdminListIdentityDocuments(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}

// AdminRecordIdentityVerdict handles POST /admin/identity-documents/{id}/verdict.
func (h *Account) AdminRecordIdentityVerdict(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, h.log)
}
