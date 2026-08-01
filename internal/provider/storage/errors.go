package storage

import (
	"net/http"

	"shopnexus/internal/shared/errx"
)

// The coded errors a store answers. Here rather than in a module's domain, because every
// module that takes an upload gets the same three answers and a caller acts on them
// identically.
var (
	// ErrObjectNotFound is a confirm for bytes that never arrived. 409 rather than 404: the
	// row is there and the caller knows it — what is missing is the upload they were told to
	// do, and the fix is to do it, not to look elsewhere.
	ErrObjectNotFound = errx.NewError(http.StatusConflict, "upload_not_completed", "no object was uploaded to that slot")
	// ErrMimeNotAllowed is a content type the platform does not store. Checked at the presign,
	// because a slot signed for anything is a slot for anything.
	ErrMimeNotAllowed = errx.NewError(http.StatusUnprocessableEntity, "mime_not_allowed", "that file type is not accepted here")
	// ErrTooLarge is a declared size over the limit, refused before a byte moves.
	ErrTooLarge = errx.NewError(http.StatusRequestEntityTooLarge, "upload_too_large", "that file is larger than this platform accepts")
)
