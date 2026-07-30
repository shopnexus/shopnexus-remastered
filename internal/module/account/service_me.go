package account

import (
	"context"
	"fmt"

	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/shared/id"
)

func (s *Service) GetMe(ctx context.Context, req accountapi.GetMeRequest) (accountapi.Me, error) {
	acc, err := s.actor(ctx, req.ActorID)
	if err != nil {
		return accountapi.Me{}, err
	}
	profile, err := s.repo.FindProfile(ctx, acc.ID)
	if err != nil {
		return accountapi.Me{}, fmt.Errorf("find profile: %w", err)
	}
	verified, err := s.repo.HasLiveVerifiedDocument(ctx, acc.ID)
	if err != nil {
		return accountapi.Me{}, fmt.Errorf("check identity verified: %w", err)
	}
	return s.toMe(ctx, acc, profile, verified), nil
}

// UpdateMe changes the caller's identifiers. The domain owns both rules that matter —
// the last identifier cannot be removed, and a new email is unverified — so this only
// applies the patch and reports what came back.
func (s *Service) UpdateMe(ctx context.Context, req accountapi.UpdateAccountRequest) (accountapi.Me, error) {
	acc, err := s.actor(ctx, req.ActorID)
	if err != nil {
		return accountapi.Me{}, err
	}
	previousEmail := acc.Email
	if req.Email.Present() {
		if err := acc.SetEmail(req.Email.Ptr()); err != nil {
			return accountapi.Me{}, err
		}
	}
	if req.Phone.Present() {
		if err := acc.SetPhone(req.Phone.Ptr()); err != nil {
			return accountapi.Me{}, err
		}
	}
	if req.Username.Present() {
		if err := acc.SetUsername(req.Username.Ptr()); err != nil {
			return accountapi.Me{}, err
		}
	}
	if err := s.repo.UpdateAccountIdentifiers(ctx, acc); err != nil {
		return accountapi.Me{}, fmt.Errorf("update account identifiers: %w", err)
	}

	profile, err := s.repo.FindProfile(ctx, acc.ID)
	if err != nil {
		return accountapi.Me{}, fmt.Errorf("find profile: %w", err)
	}
	// A new address arrives unverified, so the verification goes out with the change
	// rather than waiting for the client to ask for it.
	if acc.Email != "" && acc.Email != previousEmail {
		s.startEmailVerification(ctx, acc, profile.Locale)
	}
	verified, err := s.repo.HasLiveVerifiedDocument(ctx, acc.ID)
	if err != nil {
		return accountapi.Me{}, fmt.Errorf("check identity verified: %w", err)
	}
	return s.toMe(ctx, acc, profile, verified), nil
}

func (s *Service) UpdateProfile(ctx context.Context, req accountapi.UpdateProfileRequest) (accountapi.Profile, error) {
	profile, err := s.repo.FindProfile(ctx, req.ActorID.Int64())
	if err != nil {
		return accountapi.Profile{}, fmt.Errorf("find profile: %w", err)
	}
	if v, ok := req.Name.Get(); ok {
		profile.Name = v
	}
	if req.Description.Present() {
		profile.Description = valueOrEmpty(req.Description.Ptr())
	}
	if req.Gender.Present() {
		profile.Gender = domain.Gender(valueOrEmpty(req.Gender.Ptr()))
	}
	if req.DateOfBirth.Present() {
		profile.DateOfBirth = nil
		if v, ok := req.DateOfBirth.Get(); ok {
			dob, err := parseDate(v)
			if err != nil {
				return accountapi.Profile{}, err
			}
			profile.DateOfBirth = dob
		}
	}
	if req.AvatarResourceID.Present() {
		profile.AvatarResourceID = 0
		if v, ok := req.AvatarResourceID.Get(); ok {
			profile.AvatarResourceID = v.Int64()
		}
	}
	if v, ok := req.Country.Get(); ok {
		profile.Country = v
	}
	if v, ok := req.Locale.Get(); ok {
		profile.Locale = v
	}
	if v, ok := req.Timezone.Get(); ok {
		profile.Timezone = v
	}
	// Validated as a whole after the patch: a rule like "a birth date is not in the
	// future" is about the resulting profile, not about the field that was sent.
	if err := profile.Validate(); err != nil {
		return accountapi.Profile{}, err
	}
	if err := s.repo.UpdateProfile(ctx, profile); err != nil {
		return accountapi.Profile{}, fmt.Errorf("update profile: %w", err)
	}
	return s.toProfile(ctx, profile), nil
}

// GetPublicAccount is the seller page: deliberately narrow, and readable by anyone.
func (s *Service) GetPublicAccount(ctx context.Context, req accountapi.GetPublicAccountRequest) (accountapi.PublicAccount, error) {
	acc, err := s.repo.FindAccountByID(ctx, req.ID.Int64())
	if err != nil {
		return accountapi.PublicAccount{}, fmt.Errorf("find account by id: %w", err)
	}
	profile, err := s.repo.FindProfile(ctx, acc.ID)
	if err != nil {
		return accountapi.PublicAccount{}, fmt.Errorf("find profile: %w", err)
	}
	followers, err := s.repo.CountFollowers(ctx, acc.ID)
	if err != nil {
		return accountapi.PublicAccount{}, fmt.Errorf("count followers: %w", err)
	}
	verified, err := s.repo.HasLiveVerifiedDocument(ctx, acc.ID)
	if err != nil {
		return accountapi.PublicAccount{}, fmt.Errorf("check identity verified: %w", err)
	}
	return accountapi.PublicAccount{
		ID:               id.Of[id.Account](acc.ID),
		Name:             profile.Name,
		Description:      optional(profile.Description),
		Avatar:           s.avatar(ctx, profile.AvatarResourceID),
		IdentityVerified: verified,
		FollowerCount:    followers,
		CreatedAt:        acc.CreatedAt,
	}, nil
}

func (s *Service) ListOAuthIdentities(ctx context.Context, req accountapi.ListOAuthIdentitiesRequest) ([]accountapi.OAuthIdentity, error) {
	rows, err := s.repo.ListOAuthIdentities(ctx, req.ActorID.Int64())
	if err != nil {
		return nil, fmt.Errorf("list oauth identities: %w", err)
	}
	out := make([]accountapi.OAuthIdentity, 0, len(rows))
	for _, i := range rows {
		out = append(out, accountapi.OAuthIdentity{Provider: i.Provider, CreatedAt: i.CreatedAt})
	}
	return out, nil
}

// UnlinkOAuthIdentity is refused when it would leave the account with no way to sign in
// — no password and no other linked provider. The alternative is an account that exists
// and nobody can reach, which support cannot fix either.
func (s *Service) UnlinkOAuthIdentity(ctx context.Context, req accountapi.UnlinkOAuthIdentityRequest) error {
	if err := domain.ValidateProvider(req.Provider); err != nil {
		return err
	}
	acc, err := s.actor(ctx, req.ActorID)
	if err != nil {
		return err
	}
	linked, err := s.repo.CountOAuthIdentities(ctx, acc.ID)
	if err != nil {
		return fmt.Errorf("count oauth identities: %w", err)
	}
	if !acc.HasPassword() && linked <= 1 {
		return domain.ErrLastSignInMethod
	}
	if err := s.repo.DeleteOAuthIdentity(ctx, acc.ID, req.Provider); err != nil {
		return fmt.Errorf("delete oauth identity: %w", err)
	}
	return nil
}

// valueOrEmpty collapses a nullable patch value to the domain's "not set".
func valueOrEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
