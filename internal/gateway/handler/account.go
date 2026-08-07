package handler

import (
	"log/slog"
	"net/http"

	"github.com/go-playground/validator/v10"

	"shopnexus/internal/gateway/gwctx"
	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/common"
	"shopnexus/internal/shared/httpx"
	"shopnexus/internal/shared/id"
)

// Account serves the account module's routes: authentication, the caller's own account
// and profile, saved addresses, push devices, the notification feed and its preferences,
// the follow graph, payout identity verification, and the moderator surface.
//
// Every method does the same four things — read the request, fill in what only the
// gateway knows (the caller, the path, the page), call the service, write the result. The
// rules, including the role checks behind the admin routes, live in the service: the
// caller's role is a row in that module's table.
type Account struct {
	svc accountapi.Service
	v   *validator.Validate
	log *slog.Logger
}

func NewAccount(svc accountapi.Service, v *validator.Validate, log *slog.Logger) *Account {
	return &Account{svc: svc, v: v, log: log}
}

// ---------------------------------------------------------------- auth ------

// Register handles POST /register.
func (h *Account) Register(w http.ResponseWriter, r *http.Request) {
	var req accountapi.RegisterRequest
	if failed(w, h.log, decodeBody(r, &req)) || failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.Register(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusCreated, res)
}

// Login handles POST /login.
func (h *Account) Login(w http.ResponseWriter, r *http.Request) {
	var req accountapi.LoginRequest
	if failed(w, h.log, decodeBody(r, &req)) || failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.Login(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// LoginOAuth handles POST /login/oauth. The status is the one thing a client cannot work
// out from the body: 201 means this provider account just became a new local one, so the
// client can send it through onboarding.
func (h *Account) LoginOAuth(w http.ResponseWriter, r *http.Request) {
	var req accountapi.OAuthLoginRequest
	if failed(w, h.log, decodeBody(r, &req)) || failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.LoginOAuth(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	status := http.StatusOK
	if res.Created {
		status = http.StatusCreated
	}
	httpx.WriteData(w, status, res.Auth)
}

// RefreshToken handles POST /token/refresh.
func (h *Account) RefreshToken(w http.ResponseWriter, r *http.Request) {
	var req accountapi.RefreshRequest
	if failed(w, h.log, decodeBody(r, &req)) || failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.Refresh(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// Logout handles POST /logout.
func (h *Account) Logout(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	var req accountapi.LogoutRequest
	if failed(w, h.log, decodeOptionalBody(r, &req)) {
		return
	}
	req.ActorID = uid
	req.SessionID = gwctx.SessionID(r.Context())
	if failed(w, h.log, check(h.v, req)) || failed(w, h.log, h.svc.Logout(r.Context(), req)) {
		return
	}
	httpx.WriteNoContent(w)
}

// ChangePassword handles PUT /password.
func (h *Account) ChangePassword(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	var req accountapi.ChangePasswordRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID = uid
	req.SessionID = gwctx.SessionID(r.Context())
	if failed(w, h.log, check(h.v, req)) || failed(w, h.log, h.svc.ChangePassword(r.Context(), req)) {
		return
	}
	httpx.WriteNoContent(w)
}

// RequestPasswordReset handles POST /password/reset-requests. 202 whether or not the
// identifier exists — the service is what keeps those two indistinguishable.
func (h *Account) RequestPasswordReset(w http.ResponseWriter, r *http.Request) {
	var req accountapi.PasswordResetRequest
	if failed(w, h.log, decodeBody(r, &req)) || failed(w, h.log, check(h.v, req)) {
		return
	}
	if failed(w, h.log, h.svc.RequestPasswordReset(r.Context(), req)) {
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// ResetPassword handles POST /password/resets.
func (h *Account) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var req accountapi.PasswordResetConfirmRequest
	if failed(w, h.log, decodeBody(r, &req)) || failed(w, h.log, check(h.v, req)) {
		return
	}
	if failed(w, h.log, h.svc.ResetPassword(r.Context(), req)) {
		return
	}
	httpx.WriteNoContent(w)
}

// RequestEmailVerification handles POST /email/verification-requests.
func (h *Account) RequestEmailVerification(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	req := accountapi.RequestEmailVerificationRequest{ActorID: uid}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	if failed(w, h.log, h.svc.RequestEmailVerification(r.Context(), req)) {
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// VerifyEmail handles POST /email/verifications.
func (h *Account) VerifyEmail(w http.ResponseWriter, r *http.Request) {
	var req accountapi.EmailVerificationRequest
	if failed(w, h.log, decodeBody(r, &req)) || failed(w, h.log, check(h.v, req)) {
		return
	}
	if failed(w, h.log, h.svc.VerifyEmail(r.Context(), req)) {
		return
	}
	httpx.WriteNoContent(w)
}

// ------------------------------------------------------------------ me ------

// GetMe handles GET /me.
func (h *Account) GetMe(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	req := accountapi.GetMeRequest{ActorID: uid}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.GetMe(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// UpdateMe handles PATCH /me.
func (h *Account) UpdateMe(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	var req accountapi.UpdateAccountRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID = uid
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.UpdateMe(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// UpdateProfile handles PATCH /me/profile.
func (h *Account) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	var req accountapi.UpdateProfileRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID = uid
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.UpdateProfile(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// GetPublicAccount handles GET /accounts/{id}.
func (h *Account) GetPublicAccount(w http.ResponseWriter, r *http.Request) {
	accountID, err := pathID[id.Account](r, "id")
	if failed(w, h.log, err) {
		return
	}
	// Anonymous is a real caller here, so a missing token is not an error — it only
	// means `following` cannot be true.
	viewer, _ := actor(r)
	req := accountapi.GetPublicAccountRequest{ID: accountID, ActorID: viewer}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.GetPublicAccount(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// ListOAuthIdentities handles GET /me/oauth-identities.
func (h *Account) ListOAuthIdentities(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	req := accountapi.ListOAuthIdentitiesRequest{ActorID: uid}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.ListOAuthIdentities(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// UnlinkOAuthIdentity handles DELETE /me/oauth-identities/{provider}. The provider is a
// natural key — the slug it is known by — so it is not an opaque id.
func (h *Account) UnlinkOAuthIdentity(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	req := accountapi.UnlinkOAuthIdentityRequest{ActorID: uid, Provider: r.PathValue("provider")}
	if failed(w, h.log, check(h.v, req)) || failed(w, h.log, h.svc.UnlinkOAuthIdentity(r.Context(), req)) {
		return
	}
	httpx.WriteNoContent(w)
}

// ---------------------------------------------------------------- uploads ------

// CreateUpload handles POST /me/uploads — a slot to PUT an avatar or an identity scan into.
func (h *Account) CreateUpload(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	var req accountapi.CreateUploadRequest
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

// ConfirmUpload handles POST /me/uploads/{id}/confirmation — the bytes are at the store.
func (h *Account) ConfirmUpload(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	resourceID, err := pathID[id.Resource](r, "id")
	if failed(w, h.log, err) {
		return
	}
	req := common.ConfirmUploadRequest{ActorID: uid, ID: resourceID}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.ConfirmUpload(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// ------------------------------------------------------------ contacts ------

// ListContacts handles GET /contacts.
// ListAdministrativeAreas handles GET /administrative-areas. Unauthenticated: the browse filters on
// these codes before anybody signs in, and an address form needs them to render at all.
func (h *Account) ListAdministrativeAreas(w http.ResponseWriter, r *http.Request) {
	req := accountapi.ListAdministrativeAreasRequest{Parent: r.URL.Query().Get("parent")}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.ListAdministrativeAreas(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

func (h *Account) ListContacts(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	req := accountapi.ListContactsRequest{ActorID: uid}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.ListContacts(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// CreateContact handles POST /contacts.
func (h *Account) CreateContact(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	var req accountapi.CreateContactRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID = uid
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.CreateContact(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusCreated, res)
}

// UpdateContact handles PATCH /contacts/{id}.
func (h *Account) UpdateContact(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	contactID, err := pathID[id.Contact](r, "id")
	if failed(w, h.log, err) {
		return
	}
	var req accountapi.UpdateContactRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID, req.ID = uid, contactID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.UpdateContact(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// DeleteContact handles DELETE /contacts/{id}.
func (h *Account) DeleteContact(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	contactID, err := pathID[id.Contact](r, "id")
	if failed(w, h.log, err) {
		return
	}
	req := accountapi.DeleteContactRequest{ActorID: uid, ID: contactID}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	if failed(w, h.log, h.svc.DeleteContact(r.Context(), req)) {
		return
	}
	httpx.WriteNoContent(w)
}

// RequestContactPhoneVerification handles POST /contacts/{id}/phone/verification-requests.
func (h *Account) RequestContactPhoneVerification(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	contactID, err := pathID[id.Contact](r, "id")
	if failed(w, h.log, err) {
		return
	}
	req := accountapi.RequestContactPhoneVerificationRequest{ActorID: uid, ID: contactID}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	if failed(w, h.log, h.svc.RequestContactPhoneVerification(r.Context(), req)) {
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// VerifyContactPhone handles POST /contacts/{id}/phone/verifications. The verified flag
// belongs to the phone on a contact — the one a carrier calls — not to the account's
// sign-in phone.
func (h *Account) VerifyContactPhone(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	contactID, err := pathID[id.Contact](r, "id")
	if failed(w, h.log, err) {
		return
	}
	var req accountapi.VerifyContactPhoneRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID, req.ID = uid, contactID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.VerifyContactPhone(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// ------------------------------------------------------------- devices ------

// RegisterDevice handles PUT /devices.
func (h *Account) RegisterDevice(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	var req accountapi.RegisterDeviceRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID = uid
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.RegisterDevice(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}

	httpx.WriteData(w, http.StatusOK, res)
}

// ListDevices handles GET /me/devices.
func (h *Account) ListDevices(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	req := accountapi.ListDevicesRequest{ActorID: uid}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.ListDevices(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// DeleteDevice handles DELETE /devices/{id}.
func (h *Account) DeleteDevice(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	deviceID, err := pathID[id.Device](r, "id")
	if failed(w, h.log, err) {
		return
	}
	req := accountapi.DeleteDeviceRequest{ActorID: uid, ID: deviceID}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	if failed(w, h.log, h.svc.DeleteDevice(r.Context(), req)) {
		return
	}
	httpx.WriteNoContent(w)
}

// ------------------------------------------------------- notifications ------

// ListNotifications handles GET /notifications.
func (h *Account) ListNotifications(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	limit, err := limitParam(r)
	if failed(w, h.log, err) {
		return
	}
	unread, err := boolParam(r, "unread")
	if failed(w, h.log, err) {
		return
	}
	req := accountapi.ListNotificationsRequest{
		ActorID:  uid,
		Category: r.URL.Query().Get("category"),
		Unread:   unread,
		Cursor:   cursorParam(r),
		Limit:    limit,
	}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	page, err := h.svc.ListNotifications(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteCursor(w, http.StatusOK, page.Data, httpx.CursorMeta{NextCursor: page.NextCursor})
}

// GetNotificationUnreadCount handles GET /notifications/unread-count.
func (h *Account) GetNotificationUnreadCount(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	req := accountapi.GetUnreadCountRequest{ActorID: uid}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.GetUnreadCount(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// MarkNotificationsRead handles POST /notifications/read.
func (h *Account) MarkNotificationsRead(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	var req accountapi.MarkNotificationsReadRequest
	if failed(w, h.log, decodeOptionalBody(r, &req)) {
		return
	}
	req.ActorID = uid
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.MarkNotificationsRead(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// GetNotificationPreferences handles GET /notification-preferences.
func (h *Account) GetNotificationPreferences(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	req := accountapi.GetNotificationPreferencesRequest{ActorID: uid}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.GetNotificationPreferences(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// UpdateNotificationPreferences handles PUT /notification-preferences.
func (h *Account) UpdateNotificationPreferences(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	var req accountapi.UpdateNotificationPreferencesRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID = uid
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.UpdateNotificationPreferences(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// ------------------------------------------------------------- follows ------

// ListFollowing handles GET /me/following.
func (h *Account) ListFollowing(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	page, limit, err := pageParams(r)
	if failed(w, h.log, err) {
		return
	}
	req := accountapi.ListFollowingRequest{ActorID: uid, Page: page, Limit: limit}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.ListFollowing(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WritePage(w, http.StatusOK, res.Data, httpx.PageMeta(res.Meta))
}

// ListFollowers handles GET /accounts/{id}/followers.
func (h *Account) ListFollowers(w http.ResponseWriter, r *http.Request) {
	accountID, err := pathID[id.Account](r, "id")
	if failed(w, h.log, err) {
		return
	}
	page, limit, err := pageParams(r)
	if failed(w, h.log, err) {
		return
	}
	req := accountapi.ListFollowersRequest{AccountID: accountID, Page: page, Limit: limit}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.ListFollowers(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WritePage(w, http.StatusOK, res.Data, httpx.PageMeta(res.Meta))
}

// Follow handles PUT /follows/{accountID}.
func (h *Account) Follow(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	targetID, err := pathID[id.Account](r, "accountID")
	if failed(w, h.log, err) {
		return
	}
	req := accountapi.FollowRequest{ActorID: uid, TargetID: targetID}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	if failed(w, h.log, h.svc.Follow(r.Context(), req)) {
		return
	}
	httpx.WriteNoContent(w)
}

// Unfollow handles DELETE /follows/{accountID}.
func (h *Account) Unfollow(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	targetID, err := pathID[id.Account](r, "accountID")
	if failed(w, h.log, err) {
		return
	}
	req := accountapi.UnfollowRequest{ActorID: uid, TargetID: targetID}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	if failed(w, h.log, h.svc.Unfollow(r.Context(), req)) {
		return
	}
	httpx.WriteNoContent(w)
}

// ----------------------------------------------------------------- kyc ------

// StartIdentityVerification handles POST /identity-documents.
func (h *Account) StartIdentityVerification(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	var req accountapi.StartIdentityVerificationRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID = uid
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.StartIdentityVerification(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusCreated, res)
}

// ListIdentityDocuments handles GET /me/identity-documents.
func (h *Account) ListIdentityDocuments(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	req := accountapi.ListIdentityDocumentsRequest{ActorID: uid}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.ListIdentityDocuments(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// --------------------------------------------------------------- admin ------

// AdminListAccounts handles GET /admin/accounts.
func (h *Account) AdminListAccounts(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	page, limit, err := pageParams(r)
	if failed(w, h.log, err) {
		return
	}
	req := accountapi.AdminListAccountsRequest{
		ActorID: uid,
		Query:   r.URL.Query().Get("q"),
		Status:  r.URL.Query().Get("status"),
		Role:    r.URL.Query().Get("role"),
		Page:    page,
		Limit:   limit,
	}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.AdminListAccounts(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WritePage(w, http.StatusOK, res.Data, httpx.PageMeta(res.Meta))
}

// AdminSuspendAccount handles POST /admin/accounts/{id}/suspension.
func (h *Account) AdminSuspendAccount(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	accountID, err := pathID[id.Account](r, "id")
	if failed(w, h.log, err) {
		return
	}
	var req accountapi.SuspendAccountRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID, req.AccountID = uid, accountID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.AdminSuspendAccount(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// AdminLiftSuspension handles DELETE /admin/accounts/{id}/suspension.
func (h *Account) AdminLiftSuspension(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	accountID, err := pathID[id.Account](r, "id")
	if failed(w, h.log, err) {
		return
	}
	req := accountapi.LiftSuspensionRequest{ActorID: uid, AccountID: accountID}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.AdminLiftSuspension(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}

// AdminCreateModerator handles POST /admin/moderators.
func (h *Account) AdminCreateModerator(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	var req accountapi.CreateModeratorRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID = uid
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.AdminCreateModerator(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusCreated, res)
}

// AdminRevokeModerator handles DELETE /admin/moderators/{id}.
func (h *Account) AdminRevokeModerator(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	accountID, err := pathID[id.Account](r, "id")
	if failed(w, h.log, err) {
		return
	}
	req := accountapi.RevokeModeratorRequest{ActorID: uid, AccountID: accountID}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	if failed(w, h.log, h.svc.AdminRevokeModerator(r.Context(), req)) {
		return
	}
	httpx.WriteNoContent(w)
}

// AdminListIdentityDocuments handles GET /admin/identity-documents. The status defaults to
// pending: the queue is what a moderator opens this for.
func (h *Account) AdminListIdentityDocuments(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	page, limit, err := pageParams(r)
	if failed(w, h.log, err) {
		return
	}
	status := stringParam(r, "status", accountapi.IdentityStatusPending)
	req := accountapi.AdminListIdentityDocumentsRequest{ActorID: uid, Status: status, Page: page, Limit: limit}
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.AdminListIdentityDocuments(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WritePage(w, http.StatusOK, res.Data, httpx.PageMeta(res.Meta))
}

// AdminRecordIdentityVerdict handles POST /admin/identity-documents/{id}/verdict.
func (h *Account) AdminRecordIdentityVerdict(w http.ResponseWriter, r *http.Request) {
	uid, err := actor(r)
	if failed(w, h.log, err) {
		return
	}
	documentID, err := pathID[id.IdentityDocument](r, "id")
	if failed(w, h.log, err) {
		return
	}
	var req accountapi.IdentityVerdictRequest
	if failed(w, h.log, decodeBody(r, &req)) {
		return
	}
	req.ActorID, req.DocumentID = uid, documentID
	if failed(w, h.log, check(h.v, req)) {
		return
	}
	res, err := h.svc.AdminRecordIdentityVerdict(r.Context(), req)
	if failed(w, h.log, err) {
		return
	}
	httpx.WriteData(w, http.StatusOK, res)
}
