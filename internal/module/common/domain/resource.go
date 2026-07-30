// Package domain: common entities + pure business rules.
package domain

import (
	"time"

	"shopnexus/internal/shared/validation"
)

// RefType values a resource can be attached to (kebab-case, mirrors the
// resource_ref_type enum).
const (
	RefTypeProductSPU    = "product-spu"
	RefTypeProductSKU    = "product-sku"
	RefTypeRefund        = "refund"
	RefTypeRefundDispute = "refund-dispute"
	RefTypeComment       = "comment"
	RefTypeOrderReceipt  = "order-receipt"
)

// Resource is an uploaded file held by a storage provider. Owning rows point at
// it from their own "attachments" array; there is no join table.
type Resource struct {
	ID           int64
	UploadedByID int64  // zero for system-generated resources
	Provider     string `validate:"required"`
	ObjectKey    string `validate:"required,max=2048"`
	Mime         string `validate:"required,max=100"`
	Size         int64  `validate:"gte=0"`
	Metadata     []byte // JSON; defaults to {}
	Checksum     string
	CreatedAt    time.Time
}

func NewResource(uploadedByID int64, provider, objectKey, mime string, size int64, metadata []byte, checksum string) (Resource, error) {
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
