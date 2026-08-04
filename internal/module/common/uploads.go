package common

import (
	"context"
	"time"
)

// The two-step upload every module that takes a file performs, as a seam rather than a concrete
// store: the implementation needs a database pool and an object store, and a service test needs
// neither. `common/uploads.Store` satisfies it.
//
// The types live here rather than beside that implementation so a module's `port` package can
// name them without pulling pgx and a storage client behind it.

// UploadRequest is what a client is about to send. The filename is only kept for its extension:
// a key a caller chose is a directory traversal waiting for a backend that resolves one.
type UploadRequest struct {
	Filename string
	Mime     string
	Size     int64
}

// UploadSlot is where to PUT, what to confirm afterwards, and until when.
type UploadSlot struct {
	ResourceID int64
	URL        string
	// Headers the client must send with the PUT, when the signature covers any.
	Headers   map[string]string
	ExpiresAt time.Time
}

// Uploads is one module's own resource table plus the object store behind it.
type Uploads interface {
	// Presign reserves a row and a slot. `kind` narrows the key prefix — "listing", "review",
	// "receipt" — so an operator holding only a key can tell what it belongs to.
	Presign(ctx context.Context, uploaderID int64, kind string, req UploadRequest) (UploadSlot, error)
	// Confirm makes the upload real, with the size the store reports rather than the one the
	// client declared. Scoped to the uploader: a resource id is guessable, and confirming
	// somebody else's slot would be claiming their upload.
	Confirm(ctx context.Context, uploaderID, resourceID int64) (Resource, error)
	// Resolve turns ids into DTOs with a short-lived signed link on each. An id that names an
	// unconfirmed, deleted or other module's upload is simply absent — a row pointing at one is
	// a picture that does not render, not a page that fails.
	Resolve(ctx context.Context, ids []int64) (map[int64]ResourceDTO, error)
	// Bytes reads the objects themselves, for the caller that has to look at them rather than
	// hand out a link — a model reading a listing photo. Same scoping as Resolve, and in the
	// order asked; an id that resolves to nothing is absent rather than an error, and a row held
	// at another origin answers storage.ErrNotReadable.
	Bytes(ctx context.Context, ids []int64) ([]Blob, error)
}

// Blob is one resource's bytes, with the id they belong to so a caller can tell which photo it is
// looking at when it asked for several.
type Blob struct {
	ResourceID int64
	Mime       string
	Data       []byte
}
