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
	"shopnexus/internal/module/common"
	"shopnexus/internal/shared/id"
)

type Service struct {
	repo port.Repository
	// accounts answers two questions this module cannot: may the caller act as staff,
	// and what is a seller called. Both are rows in the account module's tables.
	accounts accountapi.Service
	// uploads is this module's own resource table plus the object store. A photo belongs to
	// the module that took the upload, and resolving one through here is what puts a live
	// link on it rather than an id nothing can render.
	uploads common.Uploads
	v       *validator.Validate
	log     *slog.Logger
}

func NewService(
	repo port.Repository,
	accounts accountapi.Service,
	uploads common.Uploads,
	v *validator.Validate,
	log *slog.Logger,
) *Service {
	return &Service{repo: repo, accounts: accounts, uploads: uploads, v: v, log: log}
}

// CreateUpload reserves a row and a signed slot for a listing photo. The client PUTs the bytes
// at the store and confirms; until then the resource resolves to nothing, so a half-finished
// upload cannot be attached to a listing.
func (s *Service) CreateUpload(ctx context.Context, req catalogapi.CreateUploadRequest) (catalogapi.UploadSlot, error) {
	if err := s.v.Struct(req); err != nil {
		return catalogapi.UploadSlot{}, err
	}
	slot, err := s.uploads.Presign(ctx, req.ActorID.Int64(), "listing", common.UploadRequest{
		Filename: req.Filename, Mime: req.Mime, Size: req.Size,
	})
	if err != nil {
		return catalogapi.UploadSlot{}, err
	}
	return catalogapi.UploadSlot{
		ResourceID: id.Of[id.Resource](slot.ResourceID),
		URL:        slot.URL,
		Headers:    slot.Headers,
		ExpiresAt:  slot.ExpiresAt,
	}, nil
}

// ConfirmUpload makes the photo real, with the size the store reports rather than the one the
// client declared. Scoped to the uploader: a resource id is guessable, and confirming somebody
// else's slot would be claiming their upload.
func (s *Service) ConfirmUpload(ctx context.Context, req catalogapi.ConfirmUploadRequest) (common.ResourceDTO, error) {
	if err := s.v.Struct(req); err != nil {
		return common.ResourceDTO{}, err
	}
	res, err := s.uploads.Confirm(ctx, req.ActorID.Int64(), req.ID.Int64())
	if err != nil {
		return common.ResourceDTO{}, err
	}
	return res.ToDTO(), nil
}

var _ catalogapi.Service = (*Service)(nil)

// requireAdmin asks the account module for the caller's role: it is a column in that
// module's table, so there is nowhere else to learn it. An admin passes every check.
func (s *Service) requireAdmin(ctx context.Context, actorID id.ID[id.Account]) error {
	me, err := s.accounts.GetMe(ctx, accountapi.GetMeRequest{ActorID: actorID})
	if err != nil {
		return fmt.Errorf("read caller role: %w", err)
	}
	if me.Role != accountapi.RoleAdmin {
		return domain.ErrAdminRequired
	}
	return nil
}

// requireModerator asks the account module for the caller's role. An admin passes every
// moderator check — a role that outranks another and still gets refused is a bug waiting to
// be filed.
func (s *Service) requireModerator(ctx context.Context, actorID id.ID[id.Account]) error {
	me, err := s.accounts.GetMe(ctx, accountapi.GetMeRequest{ActorID: actorID})
	if err != nil {
		return fmt.Errorf("read caller role: %w", err)
	}
	if me.Role != accountapi.RoleModerator && me.Role != accountapi.RoleAdmin {
		return domain.ErrModeratorRequired
	}
	return nil
}
