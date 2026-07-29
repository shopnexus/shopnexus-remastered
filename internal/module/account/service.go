// Package account implements accountapi.Service.
package account

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/module/account/port"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/token"

	"golang.org/x/crypto/bcrypt"
)

type Service struct {
	repo   port.Repository
	tokens *token.Manager
	log    *slog.Logger
}

func NewService(repo port.Repository, tokens *token.Manager, log *slog.Logger) *Service {
	return &Service{repo: repo, tokens: tokens, log: log}
}

func (s *Service) Register(ctx context.Context, req accountapi.RegisterRequest) (accountapi.Profile, error) {
	taken, err := s.repo.ExistsByEmail(ctx, req.Email)
	if err != nil {
		return accountapi.Profile{}, fmt.Errorf("check email exists: %w", err)
	}
	if taken {
		return accountapi.Profile{}, domain.ErrEmailTaken
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return accountapi.Profile{}, fmt.Errorf("hash password: %w", err)
	}
	acc, err := domain.NewAccount(req.Email, req.Name, string(hash))
	if err != nil {
		return accountapi.Profile{}, err
	}
	if err := s.repo.Create(ctx, &acc); err != nil {
		return accountapi.Profile{}, fmt.Errorf("create account: %w", err)
	}
	return toAPIProfile(acc), nil
}

func (s *Service) Login(ctx context.Context, req accountapi.LoginRequest) (accountapi.Token, error) {
	acc, err := s.repo.FindByEmail(ctx, req.Email)
	if errors.Is(err, domain.ErrAccountNotFound) {
		// Do not reveal whether the email exists — same error as a bad password.
		return accountapi.Token{}, domain.ErrInvalidCredentials
	}
	if err != nil {
		return accountapi.Token{}, fmt.Errorf("find account by email: %w", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(acc.PasswordHash), []byte(req.Password)); err != nil {
		return accountapi.Token{}, domain.ErrInvalidCredentials
	}
	// The subject is the opaque id, not the raw key: a JWT is readable by whoever
	// holds it, so putting the sequential key there would undo shared/id.
	signed, err := s.tokens.Issue(id.Of[id.Account](acc.ID).String())
	if err != nil {
		return accountapi.Token{}, fmt.Errorf("issue token: %w", err)
	}
	return accountapi.Token{AccessToken: signed}, nil
}

func (s *Service) GetProfile(ctx context.Context, req accountapi.GetProfileRequest) (accountapi.Profile, error) {
	acc, err := s.repo.FindByID(ctx, req.UserID.Int64())
	if err != nil {
		return accountapi.Profile{}, fmt.Errorf("find account by id: %w", err)
	}
	return toAPIProfile(acc), nil
}

func toAPIProfile(a domain.Account) accountapi.Profile {
	return accountapi.Profile{ID: id.Of[id.Account](a.ID), DisplayName: a.Name, Email: a.Email}
}

var _ accountapi.Service = (*Service)(nil)
