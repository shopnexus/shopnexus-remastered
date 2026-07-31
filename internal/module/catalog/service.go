// Package catalog implements catalogapi.Service — the only place that orchestrates the
// catalog domain, its repository and the other modules it reads from.
package catalog

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/go-playground/validator/v10"

	accountapi "shopnexus/internal/module/account/api"
	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/module/catalog/port"
	commonapi "shopnexus/internal/module/common/api"
	"shopnexus/internal/shared/id"
)

type Service struct {
	repo port.Repository
	// accounts answers two questions this module cannot: may the caller act as staff,
	// and what is a seller called. Both are rows in the account module's tables.
	accounts accountapi.Service
	// resources resolves image ids, which are held inline without a foreign key because
	// they live in another schema.
	resources commonapi.Service
	v         *validator.Validate
	log       *slog.Logger
}

func NewService(
	repo port.Repository,
	accounts accountapi.Service,
	resources commonapi.Service,
	v *validator.Validate,
	log *slog.Logger,
) *Service {
	return &Service{repo: repo, accounts: accounts, resources: resources, v: v, log: log}
}

var _ catalogapi.Service = (*Service)(nil)

// requireAdmin asks the account module for the caller's role: it is a column in that
// module's table, so there is nowhere else to learn it. An admin passes every check.
func (s *Service) requireAdmin(ctx context.Context, actorID id.ID[id.Account]) error {
	me, err := s.accounts.GetMe(ctx, accountapi.GetMeRequest{ActorID: actorID})
	if err != nil {
		return fmt.Errorf("read caller role: %w", err)
	}
	if me.Role != "admin" {
		return domain.ErrAdminRequired
	}
	return nil
}
