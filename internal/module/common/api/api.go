// Package commonapi is the published contract of the common service: shared
// media resources and the pluggable option registry other modules read.
package commonapi

import (
	"context"

	"shopnexus/internal/shared/id"
)

type Resource struct {
	ID        id.ID[id.Resource] `json:"id"`
	Provider  string             `json:"provider"`
	ObjectKey string             `json:"object_key"`
	Mime      string             `json:"mime"`
	Size      int64              `json:"size"`
	Checksum  string             `json:"checksum,omitempty"`
}

type Option struct {
	// ID is a natural key ('stripe-xxx', 'ghn-xxx'), not a surrogate one, so it
	// is published as-is and never encoded.
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Priority    int    `json:"priority"`
	Type        string `json:"type"`
	Provider    string `json:"provider"`
	IsEnabled   bool   `json:"is_enabled"`
}

type RegisterResourceRequest struct {
	UploadedByID id.ID[id.Account] `json:"-"` // taken from the token
	Provider     string            `json:"provider" validate:"required"`
	ObjectKey    string            `json:"object_key" validate:"required,max=2048"`
	Mime         string            `json:"mime" validate:"required,max=100"`
	Size         int64             `json:"size" validate:"gte=0"`
	Metadata     []byte            `json:"metadata,omitempty"`
	Checksum     string            `json:"checksum,omitempty"`
}

// ListOptionsRequest returns the enabled options of one type, best first.
type ListOptionsRequest struct {
	Type string `validate:"required,oneof=payment transport notification"`
}

type Service interface {
	RegisterResource(ctx context.Context, req RegisterResourceRequest) (Resource, error)
	ListOptions(ctx context.Context, req ListOptionsRequest) ([]Option, error)
}
