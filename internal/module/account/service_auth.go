package account

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"

	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/provider/notify"
	"shopnexus/internal/provider/oauth"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/token"
	"shopnexus/internal/shared/validation"
)

// Register creates a plain user. Moderators come from POST /admin/moderators — the
// role is granted, never claimed.
func (s *Service) Register(ctx context.Context, req accountapi.RegisterRequest) (accountapi.AuthResult, error) {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return accountapi.AuthResult{}, err
	}
	hash, err := hashPassword(req.Password)
	if err != nil {
		return accountapi.AuthResult{}, err
	}
	profile, err := domain.NewProfile(req.Name, req.Country, req.Locale, req.Timezone)
	if err != nil {
		return accountapi.AuthResult{}, err
	}
	acc, err := domain.NewAccount(domain.RoleUser, req.Email, req.Phone, req.Username, hash, profile)
	if err != nil {
		return accountapi.AuthResult{}, err
	}
	if err := s.repo.Create(ctx, acc); err != nil {
		return accountapi.AuthResult{}, fmt.Errorf("create account: %w", err)
	}
	// A fresh account with an email starts the verification itself, so the user does
	// not have to find the button before their address is worth anything.
	if acc.Email != nil {
		s.startEmailVerification(ctx, acc, acc.Profile.Locale)
	}
	// Nothing to verify identity against yet, so the flag is false without a query.
	return s.authResult(ctx, acc, false)
}

// Login answers the same way whichever identifier was sent, and the same way for an
// unknown account as for a wrong password: the endpoint must not be usable to find out
// who is registered.
func (s *Service) Login(ctx context.Context, req accountapi.LoginRequest) (accountapi.AuthResult, error) {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return accountapi.AuthResult{}, err
	}
	acc, err := s.repo.GetByIdentifier(ctx, domain.NormalizeIdentifier(req.Identifier))
	if err != nil {
		if errors.Is(err, domain.ErrAccountNotFound) {
			return accountapi.AuthResult{}, domain.ErrInvalidCredentials
		}
		return accountapi.AuthResult{}, fmt.Errorf("find account by identifier: %w", err)
	}
	// A provider-only account has no password to be wrong, and saying so would reveal
	// that the account exists.
	if !acc.HasPassword() {
		return accountapi.AuthResult{}, domain.ErrInvalidCredentials
	}
	// HasPassword above is what makes this deref safe.
	if err := bcrypt.CompareHashAndPassword([]byte(*acc.PasswordHash), []byte(req.Password)); err != nil {
		return accountapi.AuthResult{}, domain.ErrInvalidCredentials
	}
	// Checked after the password on purpose: an attacker guessing passwords should not
	// learn which accounts are suspended.
	if acc.IsSuspended(time.Now()) {
		return accountapi.AuthResult{}, domain.ErrAccountSuspended
	}
	return s.signIn(ctx, acc)
}

// LoginOAuth links to the account the provider's subject already names, merges into the
// one holding a provider-verified email, or creates a new one.
func (s *Service) LoginOAuth(ctx context.Context, req accountapi.OAuthLoginRequest) (accountapi.OAuthLoginResult, error) {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return accountapi.OAuthLoginResult{}, err
	}
	if err := domain.ValidateProvider(req.Provider); err != nil {
		return accountapi.OAuthLoginResult{}, err
	}
	identity, err := s.oauth.Verify(ctx, req.Provider, req.Credential)
	if err != nil {
		return accountapi.OAuthLoginResult{}, fmt.Errorf("verify oauth credential: %w", err)
	}

	acc, created, err := s.resolveOAuthAccount(ctx, req, identity)
	if err != nil {
		return accountapi.OAuthLoginResult{}, err
	}
	if acc.IsSuspended(time.Now()) {
		return accountapi.OAuthLoginResult{}, domain.ErrAccountSuspended
	}
	auth, err := s.signIn(ctx, acc)
	if err != nil {
		return accountapi.OAuthLoginResult{}, err
	}
	return accountapi.OAuthLoginResult{Auth: auth, Created: created}, nil
}

// resolveOAuthAccount is the three-way decision behind a federated sign-in, kept apart
// from the token work so each branch stays readable.
func (s *Service) resolveOAuthAccount(ctx context.Context, req accountapi.OAuthLoginRequest, identity oauth.Identity) (*domain.Account, bool, error) {
	// 1. The provider's subject is already linked: this is a plain sign-in.
	acc, err := s.repo.GetByOAuth(ctx, identity.Provider, identity.Subject)
	switch {
	case err == nil:
		return acc, false, nil
	case !errors.Is(err, domain.ErrOAuthIdentityNotFound):
		return nil, false, fmt.Errorf("get account by oauth: %w", err)
	}

	// 2. The provider vouches for an email we already know: link to that account. Only a
	// provider-*verified* address may merge — an unverified one is a claim, and honouring
	// it would let anyone take an account by signing up at a provider with its address.
	if identity.Email != "" && identity.EmailVerified {
		acc, err := s.repo.GetByEmail(ctx, domain.NormalizeIdentifier(identity.Email))
		switch {
		case err == nil:
			if err := s.linkIdentity(ctx, acc, identity); err != nil {
				return nil, false, err
			}
			return acc, false, nil
		case !errors.Is(err, domain.ErrAccountNotFound):
			return nil, false, fmt.Errorf("get account by email: %w", err)
		}
	}

	// 3. Nobody to link to: create the account. A provider that returns no email — Apple
	// with "hide my email" — still has to leave the row addressable, hence the generated
	// username.
	username := ""
	if identity.Email == "" {
		username, err = domain.GenerateUsername()
		if err != nil {
			return nil, false, err
		}
	}
	name := identity.Name
	if name == "" {
		name = username
	}
	profile, err := domain.NewProfile(name,
		orDefault(req.Country, defaultCountry),
		orDefault(req.Locale, defaultLocale),
		orDefault(req.Timezone, defaultTimezone))
	if err != nil {
		return nil, false, err
	}
	// No password: this account signs in through the provider, which is what
	// Me.has_password reports and what makes unlinking the last provider refusable.
	acc, err = domain.NewOAuthAccount(identity.Email, username, profile, identity.Provider, identity.Subject)
	if err != nil {
		return nil, false, err
	}
	// Whether the address is already verified is a fact about the provider, so the flag is
	// set on the aggregate rather than passed to the constructor.
	acc.EmailVerified = identity.EmailVerified
	// One transaction: the account, its profile and the link that made it reachable.
	if err := s.repo.Create(ctx, acc); err != nil {
		return nil, false, fmt.Errorf("create account: %w", err)
	}
	return acc, true, nil
}

// linkIdentity records a federated identity on an account that already exists.
func (s *Service) linkIdentity(ctx context.Context, acc *domain.Account, identity oauth.Identity) error {
	if _, err := acc.Link(identity.Provider, identity.Subject); err != nil {
		return err
	}
	if err := s.repo.Save(ctx, acc, acc.ID); err != nil {
		return fmt.Errorf("save account: %w", err)
	}
	return nil
}

// Refresh exchanges a refresh token for a new access token on the same session, and
// rotates the refresh token so a stolen one is usable at most once.
func (s *Service) Refresh(ctx context.Context, req accountapi.RefreshRequest) (accountapi.AuthResult, error) {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return accountapi.AuthResult{}, err
	}
	sess, err := s.sessions.Rotate(ctx, req.RefreshToken)
	if err != nil {
		return accountapi.AuthResult{}, fmt.Errorf("rotate session: %w", err)
	}
	acc, err := s.repo.Get(ctx, sess.AccountID)
	if err != nil {
		return accountapi.AuthResult{}, fmt.Errorf("get account: %w", err)
	}
	verified, err := s.repo.HasLiveVerifiedDocument(ctx, acc.ID)
	if err != nil {
		return accountapi.AuthResult{}, fmt.Errorf("check identity verified: %w", err)
	}
	access, err := s.tokens.Issue(token.Claims{
		AccountID: id.Of[id.Account](acc.ID).String(),
		SessionID: sess.ID,
	})
	if err != nil {
		return accountapi.AuthResult{}, fmt.Errorf("issue access token: %w", err)
	}
	return accountapi.AuthResult{
		AccessToken:  access,
		RefreshToken: sess.RefreshToken,
		ExpiresIn:    int(s.tokens.TTL().Seconds()),
		Account:      s.toMe(ctx, acc, verified),
	}, nil
}

// Logout revokes the caller's session, and unregisters the device it names so the phone
// stops receiving notifications for an account nobody is signed in to.
func (s *Service) Logout(ctx context.Context, req accountapi.LogoutRequest) error {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return err
	}
	if err := s.sessions.Revoke(ctx, req.SessionID); err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	if req.DeviceID == 0 {
		return nil
	}
	device, err := s.repo.FindDevice(ctx, req.DeviceID.Int64())
	if err != nil {
		if errors.Is(err, domain.ErrDeviceNotFound) {
			// The session is gone, which is what was asked for. A device that is not there
			// is not a reason to fail the logout.
			return nil
		}
		return fmt.Errorf("find device: %w", err)
	}
	if !device.Owns(req.ActorID.Int64()) {
		return domain.ErrForbidden
	}
	if err := s.repo.DeleteDevice(ctx, device.ID); err != nil {
		return fmt.Errorf("delete device: %w", err)
	}
	return nil
}

// ChangePassword requires the current password even though the caller is authenticated:
// a stolen access token must not be enough to take the account. Every other session is
// dropped, and the caller's own survives — signing someone out of the tab they are
// typing in is not a security win.
func (s *Service) ChangePassword(ctx context.Context, req accountapi.ChangePasswordRequest) error {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return err
	}
	acc, err := s.actor(ctx, req.ActorID)
	if err != nil {
		return err
	}
	if !acc.HasPassword() {
		return domain.ErrNoPassword
	}
	if err := bcrypt.CompareHashAndPassword([]byte(*acc.PasswordHash), []byte(req.CurrentPassword)); err != nil {
		return domain.ErrInvalidCredentials
	}
	hash, err := hashPassword(req.NewPassword)
	if err != nil {
		return err
	}
	acc.SetPassword(hash)
	if err := s.repo.Save(ctx, acc, acc.ID); err != nil {
		return fmt.Errorf("save account: %w", err)
	}
	if err := s.sessions.RevokeAll(ctx, acc.ID, req.SessionID); err != nil {
		return fmt.Errorf("revoke other sessions: %w", err)
	}
	return nil
}

// RequestPasswordReset always succeeds, whether or not the identifier exists: reporting
// that an address is unknown turns this endpoint into a way to enumerate accounts. The
// throttle is applied first, for the same reason.
func (s *Service) RequestPasswordReset(ctx context.Context, req accountapi.PasswordResetRequest) error {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return err
	}
	identifier := domain.NormalizeIdentifier(req.Identifier)
	if err := s.throttle(ctx, "password-reset", identifier); err != nil {
		return err
	}
	acc, err := s.repo.GetByIdentifier(ctx, identifier)
	if err != nil {
		if errors.Is(err, domain.ErrAccountNotFound) {
			return nil
		}
		return fmt.Errorf("get account by identifier: %w", err)
	}
	secret, err := mintSecret()
	if err != nil {
		return err
	}
	if err := s.putSecret(ctx, passwordResetPrefix+secret, acc.ID, passwordResetTTL); err != nil {
		return err
	}
	// An account that signs in by phone has nowhere else to receive the link.
	email, phone := "", ""
	if acc.Email != nil {
		email = *acc.Email
	} else if acc.Phone != nil {
		phone = *acc.Phone
	}
	s.send(ctx, notify.Message{
		Kind:   notify.KindPasswordReset,
		Email:  email,
		Phone:  phone,
		Token:  secret,
		Locale: acc.Profile.Locale,
	})
	return nil
}

// ResetPassword sets the new password and drops every session: whoever asked for the
// reset may well be locked out *because* someone else is signed in.
func (s *Service) ResetPassword(ctx context.Context, req accountapi.PasswordResetConfirmRequest) error {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return err
	}
	accountID, err := s.takeSecret(ctx, passwordResetPrefix+req.Token, domain.ErrInvalidResetToken)
	if err != nil {
		return err
	}
	hash, err := hashPassword(req.NewPassword)
	if err != nil {
		return err
	}
	acc, err := s.repo.Get(ctx, accountID)
	if err != nil {
		return fmt.Errorf("get account: %w", err)
	}
	acc.SetPassword(hash)
	if err := s.repo.Save(ctx, acc, acc.ID); err != nil {
		return fmt.Errorf("save account: %w", err)
	}
	if err := s.sessions.RevokeAll(ctx, accountID, ""); err != nil {
		return fmt.Errorf("revoke sessions: %w", err)
	}
	return nil
}

func (s *Service) RequestEmailVerification(ctx context.Context, req accountapi.RequestEmailVerificationRequest) error {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return err
	}
	acc, err := s.actor(ctx, req.ActorID)
	if err != nil {
		return err
	}
	if acc.Email == nil {
		return domain.ErrNoEmail
	}
	if acc.EmailVerified {
		return domain.ErrEmailAlreadyVerified
	}
	if err := s.throttle(ctx, "email-verify", *acc.Email); err != nil {
		return err
	}
	return s.startEmailVerificationErr(ctx, acc, acc.Profile.Locale)
}

func (s *Service) VerifyEmail(ctx context.Context, req accountapi.EmailVerificationRequest) error {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return err
	}
	accountID, err := s.takeSecret(ctx, emailVerifyPrefix+req.Token, domain.ErrInvalidVerificationToken)
	if err != nil {
		return err
	}
	acc, err := s.repo.Get(ctx, accountID)
	if err != nil {
		return fmt.Errorf("get account: %w", err)
	}
	acc.MarkEmailVerified()
	if err := s.repo.Save(ctx, acc, acc.ID); err != nil {
		return fmt.Errorf("save account: %w", err)
	}
	return nil
}

// --- helpers ---

// signIn opens a session and builds the auth response for an existing account.
func (s *Service) signIn(ctx context.Context, acc *domain.Account) (accountapi.AuthResult, error) {
	verified, err := s.repo.HasLiveVerifiedDocument(ctx, acc.ID)
	if err != nil {
		return accountapi.AuthResult{}, fmt.Errorf("check identity verified: %w", err)
	}
	return s.authResult(ctx, acc, verified)
}

// authResult mints the session and the access token that names it.
func (s *Service) authResult(ctx context.Context, acc *domain.Account, identityVerified bool) (accountapi.AuthResult, error) {
	sess, err := s.sessions.Create(ctx, acc.ID)
	if err != nil {
		return accountapi.AuthResult{}, fmt.Errorf("create session: %w", err)
	}
	access, err := s.tokens.Issue(token.Claims{
		AccountID: id.Of[id.Account](acc.ID).String(),
		SessionID: sess.ID,
	})
	if err != nil {
		return accountapi.AuthResult{}, fmt.Errorf("issue access token: %w", err)
	}
	return accountapi.AuthResult{
		AccessToken:  access,
		RefreshToken: sess.RefreshToken,
		ExpiresIn:    int(s.tokens.TTL().Seconds()),
		Account:      s.toMe(ctx, acc, identityVerified),
	}, nil
}

// startEmailVerification is the fire-and-forget form used where the caller did not ask
// for the message and must not be failed by it.
func (s *Service) startEmailVerification(ctx context.Context, acc *domain.Account, locale string) {
	if err := s.startEmailVerificationErr(ctx, acc, locale); err != nil {
		s.log.Error("start email verification failed", "account_id", acc.ID, "err", err)
	}
}

func (s *Service) startEmailVerificationErr(ctx context.Context, acc *domain.Account, locale string) error {
	secret, err := mintSecret()
	if err != nil {
		return err
	}
	if err := s.putSecret(ctx, emailVerifyPrefix+secret, acc.ID, emailVerifyTTL); err != nil {
		return err
	}
	// Only ever called for an account that has an address; the check is the caller's.
	email := ""
	if acc.Email != nil {
		email = *acc.Email
	}
	s.send(ctx, notify.Message{
		Kind:   notify.KindEmailVerification,
		Email:  email,
		Token:  secret,
		Locale: locale,
	})
	return nil
}

// localeOf reads the language a message should be written in, and degrades to the
// deployment default rather than failing the send.
func (s *Service) localeOf(ctx context.Context, accountID int64) string {
	profile, err := s.repo.FindProfile(ctx, accountID)
	if err != nil {
		s.log.Warn("read locale failed", "account_id", accountID, "err", err)
		return defaultLocale
	}
	return profile.Locale
}

func hashPassword(plain string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hash), nil
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
