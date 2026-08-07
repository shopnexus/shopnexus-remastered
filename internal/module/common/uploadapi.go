package common

import (
	"time"

	"shopnexus/internal/shared/id"
)

// The published shape of the two-step upload, and it is one shape: five modules each carried a
// byte-identical CreateUploadRequest, UploadSlot and ConfirmUploadRequest, down to the comments.
// Here rather than in each module's `api` package for the same reason ResourceDTO is — a listing
// photo, an avatar, a chat attachment, a piece of refund evidence and a review photo are the same
// fact, and the route that takes one differs only in which module owns the row.

// CreateUploadRequest asks for a slot to PUT a file into. The bytes never pass through the
// gateway: the client sends them straight to the store and then confirms.
//
// A module that needs more than this embeds it — see accountapi.CreateUploadRequest, whose one
// route serves both an avatar and an identity scan and so has to be told which.
type CreateUploadRequest struct {
	ActorID  id.ID[id.Account] `json:"-" validate:"required"`
	Filename string            `json:"filename" validate:"required,max=255"`
	// Mime and Size are what the client is about to send. Both are checked before a byte
	// moves: a slot signed for anything is a slot for anything.
	Mime string `json:"mime" validate:"required,max=100"`
	Size int64  `json:"size" validate:"required,gt=0"`
}

// UploadSlotDTO is where to PUT, what to confirm afterwards, and until when.
type UploadSlotDTO struct {
	ResourceID id.ID[id.Resource] `json:"resource_id"`
	URL        string             `json:"url"`
	// Headers the client must send with the PUT, when the signature covers any.
	Headers   map[string]string `json:"headers"`
	ExpiresAt time.Time         `json:"expires_at"`
}

// ToDTO is the one conversion, as on Resource: the slot crosses to the wire in exactly one shape.
func (s UploadSlot) ToDTO() UploadSlotDTO {
	return UploadSlotDTO{
		ResourceID: id.Of[id.Resource](s.ResourceID),
		URL:        s.URL,
		Headers:    s.Headers,
		ExpiresAt:  s.ExpiresAt,
	}
}

// ConfirmUploadRequest is the second step. The size is read from the store rather than taken
// from the client, so what it declared cannot become the record.
type ConfirmUploadRequest struct {
	ActorID id.ID[id.Account]  `json:"-" validate:"required"`
	ID      id.ID[id.Resource] `json:"-" validate:"required"`
}
