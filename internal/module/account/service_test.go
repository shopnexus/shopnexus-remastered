package account_test

import (
	"context"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"shopnexus/internal/infra/cache"
	"shopnexus/internal/module/account"
	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/domain"
	kycmock "shopnexus/internal/provider/kyc/mock"
	"shopnexus/internal/provider/notify"
	oauthmock "shopnexus/internal/provider/oauth/mock"
	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/id/idtest"
	"shopnexus/internal/shared/session"
	"shopnexus/internal/shared/token"
)

func TestMain(m *testing.M) { idtest.Install(); m.Run() }

// fakeNotifier records what was sent. A message that never goes out is invisible to every
// other assertion — the send is the only observable half of a verification flow.
type fakeNotifier struct {
	mu   sync.Mutex
	sent []notify.Message
}

func (f *fakeNotifier) Send(_ context.Context, m notify.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	return nil
}

func (f *fakeNotifier) sentOf(kind notify.Kind) []notify.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []notify.Message
	for _, m := range f.sent {
		if m.Kind == kind {
			out = append(out, m)
		}
	}
	return out
}

// harness is the service with the real session store, the real dev providers and an
// in-memory repository — everything except a database.
type harness struct {
	svc      *account.Service
	notes    *fakeNotifier
	repo     *fakeRepo
	uploads  *fakeUploads
	sessions *session.Store
	tokens   *token.Manager
	cache    cache.Client
}

func newHarness() *harness {
	repo := newFakeRepo()
	uploads := newFakeUploads()
	c := cache.NewInMemoryClient()
	sessions := session.New(c, time.Hour)
	tokens := token.NewManager("0123456789012345678901234567890123", 15*time.Minute)
	log := slog.New(slog.DiscardHandler)
	notes := &fakeNotifier{}
	svc := account.NewService(repo, sessions, tokens, c,
		notes, oauthmock.NewVerifier(), kycmock.NewClient(), uploads, log)
	return &harness{svc: svc, notes: notes, repo: repo, uploads: uploads, sessions: sessions, tokens: tokens, cache: c}
}

// newTestService is for a test that wants to seed or inspect a repo it built itself, rather
// than the fresh one newHarness hides inside it.
func newTestService(t *testing.T, repo *fakeRepo) *account.Service {
	t.Helper()
	uploads := newFakeUploads()
	c := cache.NewInMemoryClient()
	sessions := session.New(c, time.Hour)
	tokens := token.NewManager("0123456789012345678901234567890123", 15*time.Minute)
	log := slog.New(slog.DiscardHandler)
	notes := &fakeNotifier{}
	return account.NewService(repo, sessions, tokens, c,
		notes, oauthmock.NewVerifier(), kycmock.NewClient(), uploads, log)
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

// markEmailVerified sets the flag the way the service does, for a test that needs an
// already-verified address rather than the flow that produces one.
func (h *harness) markEmailVerified(accountID int64) error {
	ctx := context.Background()
	acc, err := h.repo.Get(ctx, accountID)
	if err != nil {
		return err
	}
	acc.MarkEmailVerified()
	return h.repo.Save(ctx, acc, acc.ID)
}

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

	acc, err := h.repo.Get(context.Background(), res.Account.ID.Int64())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if acc.PasswordHash == nil || *acc.PasswordHash == "password1" {
		t.Fatalf("password must be hashed, got %v", acc.PasswordHash)
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
	acc, _ := h.repo.Get(context.Background(), res.Account.ID.Int64())
	acc.Suspend("scam", nil)
	if err := h.repo.Save(context.Background(), acc, 0); err != nil {
		t.Fatalf("Save: %v", err)
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
	links := h.repo.oauth[existing.Account.ID.Int64()]
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
	if err := h.markEmailVerified(res.Account.ID.Int64()); err != nil {
		t.Fatalf("MarkEmailVerified: %v", err)
	}

	newEmail := "alice2@example.com"
	me, err := h.svc.UpdateMe(context.Background(), accountapi.UpdateAccountRequest{
		ActorID: res.Account.ID,
		Email:   &newEmail,
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
	// The verification goes out with the change. Nothing else observes this: the flag would
	// read the same if the message were never sent, leaving the account unverifiable until
	// the client thought to ask.
	sent := h.notes.sentOf(notify.KindEmailVerification)
	if len(sent) == 0 || sent[len(sent)-1].Email != newEmail {
		t.Fatalf("verification messages = %+v, want one to %q", sent, newEmail)
	}
	if sent[len(sent)-1].Token == "" {
		t.Error("the verification message carries no token")
	}
}

// The other half: a change that leaves the address alone must not send anything, or every
// profile edit mails the user a token they did not ask for.
func TestUpdateMe_UnchangedEmailSendsNothing(t *testing.T) {
	h := newHarness()
	res := h.register(t, registerRequest())
	before := len(h.notes.sentOf(notify.KindEmailVerification))

	username := "alice"
	if _, err := h.svc.UpdateMe(context.Background(), accountapi.UpdateAccountRequest{
		ActorID:  res.Account.ID,
		Username: &username,
	}); err != nil {
		t.Fatalf("UpdateMe: %v", err)
	}
	if got := len(h.notes.sentOf(notify.KindEmailVerification)); got != before {
		t.Errorf("verification messages = %d, want the %d from registration", got, before)
	}
}

// An absent field is left alone; the clear flag removes the value. Both have to be
// possible, which is the whole reason a PATCH field is a pointer plus a bool.
func TestUpdateMe_AbsentAndClearDiffer(t *testing.T) {
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

	// Cleared: removed, because the email is still there to sign in with.
	me, err = h.svc.UpdateMe(ctx, accountapi.UpdateAccountRequest{ActorID: res.Account.ID, ClearUsername: true})
	if err != nil {
		t.Fatalf("UpdateMe: %v", err)
	}
	if me.Username != nil {
		t.Fatalf("username = %v, want it cleared", *me.Username)
	}
}

func TestUpdateMe_LastIdentifierCannotBeRemoved(t *testing.T) {
	h := newHarness()
	res := h.register(t, registerRequest())

	_, err := h.svc.UpdateMe(context.Background(), accountapi.UpdateAccountRequest{
		ActorID:    res.Account.ID,
		ClearEmail: true,
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

	takenEmail := "bob@example.com"
	_, err := h.svc.UpdateMe(context.Background(), accountapi.UpdateAccountRequest{
		ActorID: first.Account.ID,
		Email:   &takenEmail,
	})
	if got := status(t, err); got != 409 {
		t.Fatalf("status = %d, want 409", got)
	}
}

func TestUpdateProfile_PatchesAndValidates(t *testing.T) {
	h := newHarness()
	res := h.register(t, registerRequest())
	ctx := context.Background()

	description, dob := "Bán đồ cũ", "2001-02-03"
	p, err := h.svc.UpdateProfile(ctx, accountapi.UpdateProfileRequest{
		ActorID:     res.Account.ID,
		Description: &description,
		DateOfBirth: &dob,
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
		ActorID: res.Account.ID, DateOfBirth: &future,
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
	links := h.repo.oauth[existing.Account.ID.Int64()]
	if len(links) != 0 {
		t.Fatalf("links = %d, want none", len(links))
	}
}

// A self-service change leaves a trail too, and it is written by Save rather than by a
// separate call that could be forgotten — or succeed after the change already failed.
func TestUpdateMe_RecordsTheChangeInTheAuditLog(t *testing.T) {
	h := newHarness()
	res := h.register(t, registerRequest())

	newEmail := "alice2@example.com"
	if _, err := h.svc.UpdateMe(context.Background(), accountapi.UpdateAccountRequest{
		ActorID: res.Account.ID, Email: &newEmail,
	}); err != nil {
		t.Fatalf("UpdateMe: %v", err)
	}
	if !slices.Contains(h.repo.codes(), string(domain.EmailChanged.Code)) {
		t.Fatalf("audit codes = %v, want the change recorded", h.repo.codes())
	}
}

// Two commands built on the same read: the second loses on the version check instead of
// overwriting a decision it never saw. 409, because retrying the whole command is the
// right answer and only the client knows whether it still wants to.
func TestSave_StaleAggregateIsAConflict(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	res := h.register(t, registerRequest())

	first, err := h.repo.Get(ctx, res.Account.ID.Int64())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	stale, err := h.repo.Get(ctx, res.Account.ID.Int64())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	first.SetPhone("+84901234567")
	if err := h.repo.Save(ctx, first, first.ID); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	stale.SetUsername("alice")
	if got := status(t, h.repo.Save(ctx, stale, stale.ID)); got != 409 {
		t.Fatalf("status = %d, want 409", got)
	}
}
