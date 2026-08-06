package common

import (
	"time"

	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/validation"
)

// Resource is an uploaded file held by a storage provider. Owning rows point at it from their
// own "attachments" array; there is no join table.
//
// One row per module schema: the module that took the upload owns it. A resource id therefore
// only resolves inside the module that stored it, which is why every DTO carrying one also
// carries the resolved Resource rather than expecting the client to fetch it.
type Resource struct {
	ID           int64
	UploadedByID *int64 // nil for a system-generated resource
	Provider     string `validate:"required"`
	ObjectKey    string `validate:"required,max=2048"`
	Mime         string `validate:"required,max=100"`
	Size         int64  `validate:"gte=0"`
	Metadata     []byte // JSON; defaults to {}
	Checksum     *string
	CreatedAt    time.Time

	// URL and URLExpiresAt are not columns: they are the short-lived link a storage provider
	// presigns, filled in by whoever resolved the row. Empty until a presigner exists, and a
	// consumer that needs the bytes — the KYC check reading a scan — has to treat an empty URL
	// as "not available yet" rather than as an empty object.
	URL          string
	URLExpiresAt *time.Time
}

func NewResource(uploadedByID *int64, provider, objectKey, mime string, size int64, metadata []byte, checksum *string) (Resource, error) {
	if len(metadata) == 0 {
		metadata = []byte("{}")
	}
	r := Resource{
		UploadedByID: uploadedByID,
		Provider:     provider,
		ObjectKey:    objectKey,
		Mime:         mime,
		Size:         size,
		Metadata:     metadata,
		Checksum:     checksum,
	}
	if err := validation.Default().Struct(r); err != nil {
		return Resource{}, validation.AsError(err)
	}
	return r, nil
}

// ResourceDTO is the wire form every module's api package hands out for an attachment: the
// shape is shared because a listing image, an avatar and a review photo are the same fact.
type ResourceDTO struct {
	ID        id.ID[id.Resource] `json:"id"`
	Provider  string             `json:"provider"`
	ObjectKey string             `json:"object_key"`
	Mime      string             `json:"mime"`
	Size      int64              `json:"size"`
	Checksum  string             `json:"checksum"`
	// URL is the presigned link, as on Resource above: short-lived, not an address to store.
	URL string `json:"url"`
	// URLExpiresAt is when that link stops working, so a caller can tell a stale URL from a
	// wrong one.
	URLExpiresAt *time.Time `json:"url_expires_at"`
}

// ToDTO is the one conversion, so a module does not write its own.
func (r Resource) ToDTO() ResourceDTO {
	dto := ResourceDTO{
		ID:        id.Of[id.Resource](r.ID),
		Provider:  r.Provider,
		ObjectKey: r.ObjectKey,
		Mime:      r.Mime,
		Size:      r.Size,
	}
	if r.Checksum != nil {
		dto.Checksum = *r.Checksum
	}
	dto.URL, dto.URLExpiresAt = r.URL, r.URLExpiresAt
	return dto
}
