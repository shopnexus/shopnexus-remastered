// Package common implements commonapi.Service: shared media resources and the
// pluggable option registry that other modules read.
package common

import (
	"context"
	"fmt"
	"log/slog"

	commonapi "shopnexus/internal/module/common/api"
	"shopnexus/internal/module/common/domain"
	"shopnexus/internal/module/common/port"
	"shopnexus/internal/shared/id"
)

type Service struct {
	repo port.Repository
	log  *slog.Logger
}

func NewService(repo port.Repository, log *slog.Logger) *Service {
	return &Service{repo: repo, log: log}
}

var _ commonapi.Service = (*Service)(nil)

func (s *Service) RegisterResource(ctx context.Context, req commonapi.RegisterResourceRequest) (commonapi.Resource, error) {
	res, err := domain.NewResource(req.UploadedByID.Int64(), req.Provider, req.ObjectKey, req.Mime, req.Size, req.Metadata, req.Checksum)
	if err != nil {
		return commonapi.Resource{}, err
	}
	if err := s.repo.InsertResource(ctx, &res); err != nil {
		return commonapi.Resource{}, fmt.Errorf("insert resource: %w", err)
	}
	return toAPIResource(res), nil
}

func (s *Service) ListOptions(ctx context.Context, req commonapi.ListOptionsRequest) ([]commonapi.Option, error) {
	rows, err := s.repo.ListEnabledOptions(ctx, req.Type)
	if err != nil {
		return nil, fmt.Errorf("list enabled options: %w", err)
	}
	out := make([]commonapi.Option, 0, len(rows))
	for _, o := range rows {
		out = append(out, commonapi.Option{
			ID:          o.ID,
			Name:        o.Name,
			Description: o.Description,
			Priority:    o.Priority,
			Type:        o.Type,
			Provider:    o.Provider,
			IsEnabled:   o.IsEnabled,
		})
	}
	return out, nil
}

func toAPIResource(r domain.Resource) commonapi.Resource {
	return commonapi.Resource{
		ID:        id.Of[id.Resource](r.ID),
		Provider:  r.Provider,
		ObjectKey: r.ObjectKey,
		Mime:      r.Mime,
		Size:      r.Size,
		Checksum:  r.Checksum,
	}
}
