package account_test

import (
	"context"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"shopnexus/internal/infra/cache"
	"shopnexus/internal/module/account"
	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/domain"
	commonapi "shopnexus/internal/module/common/api"
	kycmock "shopnexus/internal/provider/kyc/mock"
	notifymock "shopnexus/internal/provider/notify/mock"
	oauthmock "shopnexus/internal/provider/oauth/mock"
	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/id/idtest"
	"shopnexus/internal/shared/patch"
	"shopnexus/internal/shared/session"
	"shopnexus/internal/shared/token"
)

func TestMain(m *testing.M) { idtest.Install(); m.Run() }

// fakeResources stands in for the common module. It answers for any id unless the test
// puts one in missing, which is how the "the vendor cannot be shown this scan" path is
// reached — a resource that was never confirmed has no fetch URL.
type fakeResources struct {
	missing map[id.ID[id.Resource]]bool
}

func (fakeResources) RegisterResource(context.Context, commonapi.RegisterResourceRequest) (commonapi.Resource, error) {
	return commonapi.Resource{}, nil
}

func (f fakeResources) GetResources(_ context.Context, req commonapi.GetResourcesRequest) ([]commonapi.Resource, error) {
	out := make([]commonapi.Resource, 0, len(req.IDs))
	for _, rid := range req.IDs {
		if f.missing[rid] {
			continue
		}
		out = append(out, commonapi.Resource{
			ID:   rid,
			Mime: "image/jpeg",
			URL:  "https://storage.invalid/scans/" + rid.String(),
		})
	}
	return out, nil
}

func (fakeResources) ListOptions(context.Context, commonapi.ListOptionsRequest) ([]commonapi.Option, error) {
	return nil, nil
}

// harness is the service with the real session store, the real dev providers and an
// in-memory repository — everything except a database.
type harness struct {
	svc       *account.Service
	repo      *fakeRepo
	sessions  *session.Store
	tokens    *token.Manager
	cache     cache.Client
	resources *fakeResources
}

func newHarness() *harness {
	repo := newFakeRepo()
	c := cache.NewInMemoryClient()
	sessions := session.New(c, time.Hour)
	tokens := token.NewManager("0123456789012345678901234567890123", 15*time.Minute)
	log := slog.New(slog.DiscardHandler)
	resources := &fakeResources{missing: map[id.ID[id.Resource]]bool{}}
	svc := account.NewService(repo, sessions, tokens, c,
		notifymock.NewClient(log), oauthmock.NewVerifier(), kycmock.NewClient(), resources, log)
	return &harness{svc: svc, repo: repo, sessions: sessions, tokens: tokens, cache: c, resources: resources}
}

func registerRequest() accountapi.RegisterRequest {
	return accountapi.RegisterRequest{
		Email:    "alice@example.com",
		Password: "password1",
		Name:     "Alice",
		Country:  "VN",
		Locale:   "vi-VN",
		Timezone: "Asia/Ho_Chi_Minh",
	}
}

// register is the shortcut most tests need: an account, signed in, with a live session.
func (h *harness) register(t *testing.T, req accountapi.RegisterRequest) accountapi.AuthResult {
	t.Helper()
	res, err := h.svc.Register(context.Background(), req)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	return res
}

func (h *harness) sessionID(t *testing.T, accessToken string) string {
	t.Helper()
	claims, err := h.tokens.Parse(accessToken)
	if err != nil {
		t.Fatalf("parse access token: %v", err)
	}
	return claims.SessionID
}

func status(t *testing.T, err error) uint16 {
	t.Helper()
	s, _, _, ok := errx.Decompose(err)
	if !ok {
		t.Fatalf("expected a coded error, got %v", err)
	}
	return s
}

func mustErr[T any](_ T, err error) error { return err }

// storedPhoneCode reads back the code the service minted for a contact. The key is spelled
// out here because it is private to the service — a test that wants to finish the SMS flow
// has to look where the real one does.
func (h *harness) storedPhoneCode(contactID int64) (string, bool) {
	var code string
	key := "account:contact-phone-code:" + strconv.FormatInt(contactID, 10)
	if err := h.cache.Get(context.Background(), key, &code); err != nil {
		return "", false
	}
	return code, true
}

// --- registration and sign-in ---

func TestRegister_ReturnsASignedInAccount(t *testing.T) {
	h := newHarness()
	res := h.register(t, registerRequest())

	if res.AccessToken == "" || res.RefreshToken == "" {
		t.Fatal("expected both tokens")
	}
	if res.ExpiresIn != int((15 * time.Minute).Seconds()) {
		t.Errorf("expires_in = %d, want the access token's lifetime", res.ExpiresIn)
	}
	if res.Account.Profile.Name != "Alice" || res.Account.Role != string(domain.RoleUser) {
		t.Errorf("account = %+v, want a plain user named Alice", res.Account)
	}
	// The profile is created with the account, so an account with no display name is never a
	// state a client can observe.
	if _, err := h.repo.FindProfile(context.Background(), res.Account.ID.Int64()); err != nil {
		t.Errorf("profile was not created with the account: %v", err)
	}
	if res.Account.EmailVerified {
		t.Error("a fresh email must not start out verified")
	}
	// The session is live, which is what the gateway checks on the next request.
	if _, err := h.sessions.Lookup(context.Background(), h.sessionID(t, res.AccessToken)); err != nil {
		t.Errorf("session is not usable: %v", err)
	}
}

func TestRegister_PasswordIsHashed(t *testing.T) {
	h := newHarness()
	res := h.register(t, registerRequest())

	acc, err := h.repo.FindAccountByID(context.Background(), res.Account.ID.Int64())
	if err != nil {
		t.Fatalf("FindAccountByID: %v", err)
	}
	if acc.PasswordHash == "" || acc.PasswordHash == "password1" {
		t.Fatalf("password must be hashed, got %q", acc.PasswordHash)
	}
}

func TestRegister_DuplicateIdentifierConflicts(t *testing.T) {
	h := newHarness()
	h.register(t, registerRequest())

	_, err := h.svc.Register(context.Background(), registerRequest())
	if got := status(t, err); got != 409 {
		t.Fatalf("status = %d, want 409", got)
	}
}

// Any of the three identifiers signs in, and which one it was is not reported back.
func TestLogin_ByAnyIdentifier(t *testing.T) {
	h := newHarness()
	req := registerRequest()
	req.Phone, req.Username = "+84901234567", "alice"
	h.register(t, req)

	for _, identifier := range []string{"alice@example.com", "+84901234567", "alice", "ALICE@example.com"} {
		if _, err := h.svc.Login(context.Background(), accountapi.LoginRequest{Identifier: identifier, Password: "password1"}); err != nil {
			t.Errorf("Login(%q): %v", identifier, err)
		}
	}
}

// An unknown account and a wrong password are the same answer: anything else turns login
// into a way to find out who is registered.
func TestLogin_UnknownAndWrongPasswordAreIndistinguishable(t *testing.T) {
	h := newHarness()
	h.register(t, registerRequest())

	_, wrongErr := h.svc.Login(context.Background(), accountapi.LoginRequest{Identifier: "alice@example.com", Password: "nope"})
	_, unknownErr := h.svc.Login(context.Background(), accountapi.LoginRequest{Identifier: "nobody@example.com", Password: "password1"})

	for _, err := range []error{wrongErr, unknownErr} {
		if got := status(t, err); got != 401 {
			t.Fatalf("status = %d, want 401 (%v)", got, err)
		}
	}
	if wrongErr.Error() != unknownErr.Error() {
		t.Fatalf("the two answers differ:\n wrong password: %v\n unknown account: %v", wrongErr, unknownErr)
	}
}

func TestLogin_SuspendedAccountRefused(t *testing.T) {
	h := newHarness()
	res := h.register(t, registerRequest())
	acc, _ := h.repo.FindAccountByID(context.Background(), res.Account.ID.Int64())
	acc.Suspend("scam", nil)
	if err := h.repo.UpdateAccountStatus(context.Background(), acc); err != nil {
		t.Fatalf("UpdateAccountStatus: %v", err)
	}

	_, err := h.svc.Login(context.Background(), accountapi.LoginRequest{Identifier: "alice@example.com", Password: "password1"})
	if got := status(t, err); got != 403 {
		t.Fatalf("status = %d, want 403", got)
	}
}

// --- federated sign-in ---

// The provider's subject is what identifies the account, so signing in twice links once.
func TestLoginOAuth_CreatesThenReuses(t *testing.T) {
	h := newHarness()
	req := accountapi.OAuthLoginRequest{Provider: "google", Credential: "sub-1:alice@example.com"}

	first, err := h.svc.LoginOAuth(context.Background(), req)
	if err != nil {
		t.Fatalf("first LoginOAuth: %v", err)
	}
	if !first.Created {
		t.Error("the first federated sign-in should have created an account")
	}
	// A provider-verified email arrives verified: the provider already checked it.
	if !first.Auth.Account.EmailVerified {
		t.Error("a provider-verified email should not need verifying again")
	}
	// No password, which is what makes unlinking the last provider refusable.
	if first.Auth.Account.HasPassword {
		t.Error("a provider-only account must have no password")
	}

	second, err := h.svc.LoginOAuth(context.Background(), req)
	if err != nil {
		t.Fatalf("second LoginOAuth: %v", err)
	}
	if second.Created {
		t.Error("the second sign-in created a second account")
	}
	if second.Auth.Account.ID != first.Auth.Account.ID {
		t.Errorf("account = %v, want %v", second.Auth.Account.ID, first.Auth.Account.ID)
	}
}

// A provider-verified email merges into the account that already holds it, rather than
// creating a second account nobody can tell apart.
func TestLoginOAuth_MergesOnVerifiedEmail(t *testing.T) {
	h := newHarness()
	existing := h.register(t, registerRequest())

	res, err := h.svc.LoginOAuth(context.Background(), accountapi.OAuthLoginRequest{
		Provider: "google", Credential: "sub-1:alice@example.com",
	})
	if err != nil {
		t.Fatalf("LoginOAuth: %v", err)
	}
	if res.Created {
		t.Error("a known email should link, not create")
	}
	if res.Auth.Account.ID != existing.Account.ID {
		t.Errorf("account = %v, want the existing %v", res.Auth.Account.ID, existing.Account.ID)
	}
	links, _ := h.repo.ListOAuthIdentities(context.Background(), existing.Account.ID.Int64())
	if len(links) != 1 {
		t.Fatalf("links = %d, want the provider linked to the existing account", len(links))
	}
}

// Apple's "hide my email": no address at all, so the account still has to end up
// addressable — by a generated username.
func TestLoginOAuth_NoEmailGetsAUsername(t *testing.T) {
	h := newHarness()
	res, err := h.svc.LoginOAuth(context.Background(), accountapi.OAuthLoginRequest{Provider: "apple", Credential: "sub-9"})
	if err != nil {
		t.Fatalf("LoginOAuth: %v", err)
	}
	if res.Auth.Account.Username == nil || *res.Auth.Account.Username == "" {
		t.Fatalf("account = %+v, want a generated username", res.Auth.Account)
	}
	if res.Auth.Account.Email != nil {
		t.Errorf("email = %v, want null", *res.Auth.Account.Email)
	}
}

// --- sessions ---

func TestRefresh_RotatesAndKeepsTheSession(t *testing.T) {
	h := newHarness()
	first := h.register(t, registerRequest())

	second, err := h.svc.Refresh(context.Background(), accountapi.RefreshRequest{RefreshToken: first.RefreshToken})
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if second.RefreshToken == first.RefreshToken {
		t.Error("refresh token was not rotated")
	}
	if h.sessionID(t, second.AccessToken) != h.sessionID(t, first.AccessToken) {
		t.Error("refreshing changed the session, which a logout would then miss")
	}
	if _, err := h.svc.Refresh(context.Background(), accountapi.RefreshRequest{RefreshToken: first.RefreshToken}); status(t, err) != 401 {
		t.Errorf("the used refresh token still works: %v", err)
	}
}

func TestLogout_RevokesTheCallersSession(t *testing.T) {
	h := newHarness()
	res := h.register(t, registerRequest())
	sid := h.sessionID(t, res.AccessToken)

	if err := h.svc.Logout(context.Background(), accountapi.LogoutRequest{ActorID: res.Account.ID, SessionID: sid}); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := h.sessions.Lookup(context.Background(), sid); err == nil {
		t.Fatal("the session survived a logout")
	}
}

// The device goes with the session: otherwise the phone keeps receiving notifications for an
// account nobody is signed in to.
func TestLogout_UnregistersTheNamedDevice(t *testing.T) {
	h := newHarness()
	res := h.register(t, registerRequest())
	device, err := h.svc.RegisterDevice(context.Background(), accountapi.RegisterDeviceRequest{
		ActorID: res.Account.ID, Platform: "ios", PushToken: "token-1",
	})
	if err != nil {
		t.Fatalf("RegisterDevice: %v", err)
	}

	err = h.svc.Logout(context.Background(), accountapi.LogoutRequest{
		ActorID: res.Account.ID, SessionID: h.sessionID(t, res.AccessToken), DeviceID: device.ID,
	})
	if err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := h.repo.FindDevice(context.Background(), device.ID.Int64()); err == nil {
		t.Fatal("the device was not unregistered")
	}
}

// A password change signs the account out everywhere else, and leaves the caller signed in.
func TestChangePassword_KeepsThisSessionAndDropsTheOthers(t *testing.T) {
	h := newHarness()
	first := h.register(t, registerRequest())
	second, err := h.svc.Login(context.Background(), accountapi.LoginRequest{Identifier: "alice@example.com", Password: "password1"})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	err = h.svc.ChangePassword(context.Background(), accountapi.ChangePasswordRequest{
		ActorID:         first.Account.ID,
		SessionID:       h.sessionID(t, first.AccessToken),
		CurrentPassword: "password1",
		NewPassword:     "password2",
	})
	if err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if _, err := h.sessions.Lookup(context.Background(), h.sessionID(t, first.AccessToken)); err != nil {
		t.Errorf("the caller's own session was dropped: %v", err)
	}
	if _, err := h.sessions.Lookup(context.Background(), h.sessionID(t, second.AccessToken)); err == nil {
		t.Error("another session survived the password change")
	}
	if _, err := h.svc.Login(context.Background(), accountapi.LoginRequest{Identifier: "alice@example.com", Password: "password2"}); err != nil {
		t.Errorf("the new password does not work: %v", err)
	}
}

// A stolen access token must not be enough to take the account.
func TestChangePassword_WrongCurrentPasswordRefused(t *testing.T) {
	h := newHarness()
	res := h.register(t, registerRequest())

	err := h.svc.ChangePassword(context.Background(), accountapi.ChangePasswordRequest{
		ActorID:         res.Account.ID,
		SessionID:       h.sessionID(t, res.AccessToken),
		CurrentPassword: "not-it",
		NewPassword:     "password2",
	})
	if got := status(t, err); got != 401 {
		t.Fatalf("status = %d, want 401", got)
	}
}

// A provider-only account has no password to change.
func TestChangePassword_ProviderOnlyAccount(t *testing.T) {
	h := newHarness()
	res, err := h.svc.LoginOAuth(context.Background(), accountapi.OAuthLoginRequest{Provider: "google", Credential: "sub-1:a@b.com"})
	if err != nil {
		t.Fatalf("LoginOAuth: %v", err)
	}
	err = h.svc.ChangePassword(context.Background(), accountapi.ChangePasswordRequest{
		ActorID:         res.Auth.Account.ID,
		SessionID:       h.sessionID(t, res.Auth.AccessToken),
		CurrentPassword: "whatever",
		NewPassword:     "password2",
	})
	if got := status(t, err); got != 422 {
		t.Fatalf("status = %d, want 422", got)
	}
}

// --- password reset ---

// The endpoint answers the same way for an unknown identifier, or it becomes a way to
// enumerate accounts.
func TestRequestPasswordReset_SucceedsForUnknownIdentifier(t *testing.T) {
	h := newHarness()
	if err := h.svc.RequestPasswordReset(context.Background(), accountapi.PasswordResetRequest{Identifier: "nobody@example.com"}); err != nil {
		t.Fatalf("RequestPasswordReset: %v", err)
	}
}

// The throttle is keyed on the identifier and applied before the lookup, so a 429 says
// nothing about who exists.
func TestRequestPasswordReset_ThrottledPerIdentifier(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	req := accountapi.PasswordResetRequest{Identifier: "nobody@example.com"}

	if err := h.svc.RequestPasswordReset(ctx, req); err != nil {
		t.Fatalf("first request: %v", err)
	}
	if got := status(t, h.svc.RequestPasswordReset(ctx, req)); got != 429 {
		t.Fatalf("status = %d, want 429", got)
	}
}

func TestResetPassword_UnknownTokenRefused(t *testing.T) {
	h := newHarness()
	err := h.svc.ResetPassword(context.Background(), accountapi.PasswordResetConfirmRequest{Token: "nope", NewPassword: "password2"})
	if got := status(t, err); got != 401 {
		t.Fatalf("status = %d, want 401", got)
	}
}

// --- the caller's own account ---

func TestGetMe_ReportsTheIdentityFlags(t *testing.T) {
	h := newHarness()
	res := h.register(t, registerRequest())

	me, err := h.svc.GetMe(context.Background(), accountapi.GetMeRequest{ActorID: res.Account.ID})
	if err != nil {
		t.Fatalf("GetMe: %v", err)
	}
	if !me.HasPassword {
		t.Error("has_password = false for a password account")
	}
	if me.IdentityVerified {
		t.Error("identity_verified = true with no verified document")
	}
}

// Changing the email clears the verified flag.
func TestUpdateMe_NewEmailIsUnverified(t *testing.T) {
	h := newHarness()
	res := h.register(t, registerRequest())
	if err := h.repo.MarkEmailVerified(context.Background(), res.Account.ID.Int64()); err != nil {
		t.Fatalf("MarkEmailVerified: %v", err)
	}

	me, err := h.svc.UpdateMe(context.Background(), accountapi.UpdateAccountRequest{
		ActorID: res.Account.ID,
		Email:   patch.Of("alice2@example.com"),
	})
	if err != nil {
		t.Fatalf("UpdateMe: %v", err)
	}
	if me.Email == nil || *me.Email != "alice2@example.com" {
		t.Fatalf("email = %v", me.Email)
	}
	if me.EmailVerified {
		t.Error("email_verified survived an address change")
	}
}

// An absent field is left alone; null removes the value. Both have to be possible, which is
// what patch.Field is for.
func TestUpdateMe_AbsentAndNullDiffer(t *testing.T) {
	h := newHarness()
	req := registerRequest()
	req.Username = "alice"
	res := h.register(t, req)
	ctx := context.Background()

	me, err := h.svc.UpdateMe(ctx, accountapi.UpdateAccountRequest{ActorID: res.Account.ID})
	if err != nil {
		t.Fatalf("UpdateMe: %v", err)
	}
	if me.Username == nil || *me.Username != "alice" {
		t.Fatalf("username = %v, want it untouched", me.Username)
	}

	// Null: removed, because the email is still there to sign in with.
	me, err = h.svc.UpdateMe(ctx, accountapi.UpdateAccountRequest{ActorID: res.Account.ID, Username: patch.Clear[string]()})
	if err != nil {
		t.Fatalf("UpdateMe: %v", err)
	}
	if me.Username != nil {
		t.Fatalf("username = %v, want null", *me.Username)
	}
}

func TestUpdateMe_LastIdentifierCannotBeRemoved(t *testing.T) {
	h := newHarness()
	res := h.register(t, registerRequest())

	_, err := h.svc.UpdateMe(context.Background(), accountapi.UpdateAccountRequest{
		ActorID: res.Account.ID,
		Email:   patch.Clear[string](),
	})
	if got := status(t, err); got != 422 {
		t.Fatalf("status = %d, want 422", got)
	}
}

func TestUpdateMe_TakenIdentifierConflicts(t *testing.T) {
	h := newHarness()
	first := h.register(t, registerRequest())
	other := registerRequest()
	other.Email = "bob@example.com"
	h.register(t, other)

	_, err := h.svc.UpdateMe(context.Background(), accountapi.UpdateAccountRequest{
		ActorID: first.Account.ID,
		Email:   patch.Of("bob@example.com"),
	})
	if got := status(t, err); got != 409 {
		t.Fatalf("status = %d, want 409", got)
	}
}

func TestUpdateProfile_PatchesAndValidates(t *testing.T) {
	h := newHarness()
	res := h.register(t, registerRequest())
	ctx := context.Background()

	p, err := h.svc.UpdateProfile(ctx, accountapi.UpdateProfileRequest{
		ActorID:     res.Account.ID,
		Description: patch.Of("Bán đồ cũ"),
		DateOfBirth: patch.Of("2001-02-03"),
	})
	if err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	if p.Description == nil || *p.Description != "Bán đồ cũ" {
		t.Errorf("description = %v", p.Description)
	}
	if p.DateOfBirth == nil || *p.DateOfBirth != "2001-02-03" {
		t.Errorf("date_of_birth = %v", p.DateOfBirth)
	}
	// A field the patch does not mention is untouched.
	if p.Name != "Alice" {
		t.Errorf("name = %q, want Alice", p.Name)
	}

	// A birth date in the future is a rule about the resulting profile, not about the field.
	future := time.Now().AddDate(1, 0, 0).Format(time.DateOnly)
	err = mustErr(h.svc.UpdateProfile(ctx, accountapi.UpdateProfileRequest{
		ActorID: res.Account.ID, DateOfBirth: patch.Of(future),
	}))
	if got := status(t, err); got != 400 {
		t.Errorf("status = %d, want 400 for a future birth date", got)
	}
}

// --- unlinking a provider ---

// Refused when it would leave the account with no way to sign in: the alternative is an
// account that exists and nobody can reach.
func TestUnlinkOAuthIdentity_LastSignInMethodRefused(t *testing.T) {
	h := newHarness()
	res, err := h.svc.LoginOAuth(context.Background(), accountapi.OAuthLoginRequest{Provider: "google", Credential: "sub-1:a@b.com"})
	if err != nil {
		t.Fatalf("LoginOAuth: %v", err)
	}

	err = h.svc.UnlinkOAuthIdentity(context.Background(), accountapi.UnlinkOAuthIdentityRequest{
		ActorID: res.Auth.Account.ID, Provider: "google",
	})
	if got := status(t, err); got != 422 {
		t.Fatalf("status = %d, want 422", got)
	}
}

func TestUnlinkOAuthIdentity_AllowedWithAPassword(t *testing.T) {
	h := newHarness()
	existing := h.register(t, registerRequest())
	if _, err := h.svc.LoginOAuth(context.Background(), accountapi.OAuthLoginRequest{
		Provider: "google", Credential: "sub-1:alice@example.com",
	}); err != nil {
		t.Fatalf("LoginOAuth: %v", err)
	}

	if err := h.svc.UnlinkOAuthIdentity(context.Background(), accountapi.UnlinkOAuthIdentityRequest{
		ActorID: existing.Account.ID, Provider: "google",
	}); err != nil {
		t.Fatalf("UnlinkOAuthIdentity: %v", err)
	}
	links, _ := h.repo.ListOAuthIdentities(context.Background(), existing.Account.ID.Int64())
	if len(links) != 0 {
		t.Fatalf("links = %d, want none", len(links))
	}
}
