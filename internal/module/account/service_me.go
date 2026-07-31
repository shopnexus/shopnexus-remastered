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
	verified, err := s.repo.HasLiveVerifiedDocument(ctx, acc.ID)
	if err != nil {
		return accountapi.Me{}, fmt.Errorf("check identity verified: %w", err)
	}
	return s.toMe(ctx, acc, verified), nil
}

// UpdateMe changes the caller's identifiers. The domain owns both rules that matter — the
// last identifier cannot be removed, and a new email is unverified — so this applies the
// patch and reports what came back.
func (s *Service) UpdateMe(ctx context.Context, req accountapi.UpdateAccountRequest) (accountapi.Me, error) {
	acc, err := s.actor(ctx, req.ActorID)
	if err != nil {
		return accountapi.Me{}, err
	}
	// Absent leaves the identifier alone, a value replaces it, the flag removes it.
	switch {
	case req.ClearEmail:
		acc.ClearEmail()
	case req.Email != nil:
		acc.SetEmail(*req.Email)
	}
	switch {
	case req.ClearPhone:
		acc.ClearPhone()
	case req.Phone != nil:
		acc.SetPhone(*req.Phone)
	}
	switch {
	case req.ClearUsername:
		acc.ClearUsername()
	case req.Username != nil:
		acc.SetUsername(*req.Username)
	}
	if err := s.repo.Save(ctx, acc, acc.ID); err != nil {
		return accountapi.Me{}, fmt.Errorf("save account: %w", err)
	}
	// A new address arrives unverified, so the verification goes out with the change
	// rather than waiting for the client to ask for it.
	if acc.Happened(domain.EmailChanged.Code) && acc.Email != nil {
		s.startEmailVerification(ctx, acc, acc.Profile.Locale)
	}
	verified, err := s.repo.HasLiveVerifiedDocument(ctx, acc.ID)
	if err != nil {
		return accountapi.Me{}, fmt.Errorf("check identity verified: %w", err)
	}
	return s.toMe(ctx, acc, verified), nil
}

func (s *Service) UpdateProfile(ctx context.Context, req accountapi.UpdateProfileRequest) (accountapi.Profile, error) {
	acc, err := s.actor(ctx, req.ActorID)
	if err != nil {
		return accountapi.Profile{}, err
	}
	// A required column has no clear flag: absent leaves it, a value replaces it.
	p := &acc.Profile
	if req.Name != nil {
		p.Name = *req.Name
	}
	if req.Country != nil {
		p.Country = *req.Country
	}
	if req.Locale != nil {
		p.Locale = *req.Locale
	}
	if req.Timezone != nil {
		p.Timezone = *req.Timezone
	}
	// A nullable one has one, so each is absent / value / cleared.
	switch {
	case req.ClearDescription:
		p.Description = nil
	case req.Description != nil:
		p.Description = req.Description
	}
	switch {
	case req.ClearGender:
		p.Gender = nil
	case req.Gender != nil:
		g := domain.Gender(*req.Gender)
		p.Gender = &g
	}
	switch {
	case req.ClearAvatarResourceID:
		p.AvatarResourceID = nil
	case req.AvatarResourceID != nil:
		rid := req.AvatarResourceID.Int64()
		p.AvatarResourceID = &rid
	}
	switch {
	case req.ClearDateOfBirth:
		p.DateOfBirth = nil
	case req.DateOfBirth != nil:
		dob, err := parseDate(*req.DateOfBirth) // a bad date is a 400 before any rule
		if err != nil {
			return accountapi.Profile{}, err
		}
		p.DateOfBirth = dob
	}
	// Save validates the whole aggregate, so "a birth date is not in the future" is
	// checked against the result rather than the field.
	if err := s.repo.Save(ctx, acc, acc.ID); err != nil {
		return accountapi.Profile{}, fmt.Errorf("save account: %w", err)
	}
	return s.toProfile(ctx, acc), nil
}

// GetPublicAccount is the seller page: deliberately narrow, and readable by anyone.
func (s *Service) GetPublicAccount(ctx context.Context, req accountapi.GetPublicAccountRequest) (accountapi.PublicAccount, error) {
	acc, err := s.repo.Get(ctx, req.ID.Int64())
	if err != nil {
		return accountapi.PublicAccount{}, fmt.Errorf("get account: %w", err)
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
		Name:             acc.Profile.Name,
		Description:      acc.Profile.Description,
		Avatar:           s.avatar(ctx, acc.Profile.AvatarResourceID),
		IdentityVerified: verified,
		FollowerCount:    followers,
		CreatedAt:        acc.CreatedAt,
	}, nil
}

func (s *Service) ListOAuthIdentities(ctx context.Context, req accountapi.ListOAuthIdentitiesRequest) ([]accountapi.OAuthIdentity, error) {
	acc, err := s.repo.Get(ctx, req.ActorID.Int64())
	if err != nil {
		return nil, fmt.Errorf("get account: %w", err)
	}
	out := make([]accountapi.OAuthIdentity, 0, len(acc.Identities))
	for _, i := range acc.Identities {
		out = append(out, accountapi.OAuthIdentity{Provider: i.Provider, CreatedAt: i.CreatedAt})
	}
	return out, nil
}

// UnlinkOAuthIdentity is refused when it would leave the account with no way to sign in.
// The rule is the root's, and the version check is what makes it stick: two concurrent
// unlinks of different providers cannot both read "there is another way in" and win.
func (s *Service) UnlinkOAuthIdentity(ctx context.Context, req accountapi.UnlinkOAuthIdentityRequest) error {
	if err := domain.ValidateProvider(req.Provider); err != nil {
		return err
	}
	acc, err := s.repo.Get(ctx, req.ActorID.Int64())
	if err != nil {
		return fmt.Errorf("get account: %w", err)
	}
	if err := acc.Unlink(req.Provider); err != nil {
		return err
	}
	if err := s.repo.Save(ctx, acc, acc.ID); err != nil {
		return fmt.Errorf("save account: %w", err)
	}
	return nil
}
