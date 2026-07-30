// Package commonapi is the published contract of the common service: shared
// media resources and the pluggable option registry other modules read.
package commonapi

import (
	"context"
	"time"

	"shopnexus/internal/shared/id"
)

type Resource struct {
	ID        id.ID[id.Resource] `json:"id"`
	Provider  string             `json:"provider"`
	ObjectKey string             `json:"object_key"`
	Mime      string             `json:"mime"`
	Size      int64              `json:"size"`
	Checksum  string             `json:"checksum,omitempty"`
	// URL is a short-lived link to the bytes, issued by the storage provider. Not a
	// stable address: store the id, not this. It is empty until this module can presign
	// one, and a consumer that needs the bytes — the KYC check reading a scan — has to
	// treat an empty URL as "not available yet" rather than as an empty object.
	URL string `json:"url,omitempty"`
	// URLExpiresAt is when that link stops working, so a caller can tell a stale URL
	// from a wrong one.
	URLExpiresAt *time.Time `json:"url_expires_at,omitempty"`
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

// GetResourcesRequest reads several resources at once. Batched rather than one call
// per id because the callers are list views — a page of twenty sellers is twenty
// avatars, and that has to be one query.
type GetResourcesRequest struct {
	IDs []id.ID[id.Resource] `validate:"required,min=1"`
}

type Service interface {
	RegisterResource(ctx context.Context, req RegisterResourceRequest) (Resource, error)
	// GetResources returns the resources that exist, in no guaranteed order. A missing
	// id is simply absent from the result: a row pointing at a deleted resource is a
	// picture that does not render, not an error that fails the page.
	GetResources(ctx context.Context, req GetResourcesRequest) ([]Resource, error)
	ListOptions(ctx context.Context, req ListOptionsRequest) ([]Option, error)
}
