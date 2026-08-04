// Package storage is the seam every uploaded byte goes through: a listing photo, a review
// photo, unboxing evidence, an identity scan.
//
// The bytes never pass through this process. A caller asks for a presigned PUT, hands it to
// the client, and the client uploads straight to the object store; a second call confirms the
// row once the object is there. That is why `resource` has a `completed_at` and an index over
// the ones that lack it — an upload that was started and abandoned is a row to reap, not a
// request to keep open. Streaming the bytes through the API instead would put a multi-megabyte
// body on every request path and make the gateway the thing that has to scale with photos.
package storage

import (
	"context"
	"time"
)

// Upload is one presigned slot: where the client PUTs, under what key, and until when.
type Upload struct {
	// URL is the presigned target. Short-lived by construction — a link that outlives the
	// page it was rendered for is a writable hole in the bucket.
	URL       string
	ObjectKey string
	ExpiresAt time.Time
	// Headers the client must send back verbatim, when the signature covers any. Empty for a
	// provider that signs the method and the key alone.
	Headers map[string]string
}

// NewUpload is what a module asks for. The prefix keeps one module's objects together — and
// makes an object's owner readable from its key, which is what an operator needs when they are
// holding a key and nothing else.
type NewUpload struct {
	Prefix string
	// Filename is only used to keep the extension; the stored key is generated, because a
	// client-supplied path is a directory traversal waiting for a provider that resolves it.
	Filename string
	Mime     string
	// Size is what the client says it will upload. Signed into the request where the provider
	// supports it, so the slot cannot be used for a different, larger object.
	Size int64
}

// Blob is an object's bytes with the type they are. Held whole rather than streamed: the one
// caller reads a photo to send it somewhere, and a store that has to buffer anyway gains nothing
// from a reader it would only drain.
type Blob struct {
	Mime string
	Data []byte
}

// Object is what the store knows about a key, which is how a confirm step checks that the
// bytes actually arrived rather than trusting the client that says they did.
type Object struct {
	ObjectKey string
	Size      int64
	Mime      string
	// Checksum is the store's own, when it keeps one. Empty otherwise.
	Checksum string
}

// Client is the store. Every method takes a context and applies its own per-operation
// deadline: how long a bucket is allowed to take is the provider's knowledge, not the
// caller's.
type Client interface {
	// Name is the value written to `resource.provider`, so a row can be traced back to the
	// bucket that holds it. kebab-case.
	Name() string
	// PresignUpload issues a slot for the client to PUT to.
	PresignUpload(ctx context.Context, req NewUpload) (Upload, error)
	// Stat is the confirm step's check: the object exists and this is what it is. It answers
	// ErrObjectNotFound when the client never uploaded.
	Stat(ctx context.Context, objectKey string) (Object, error)
	// PresignDownload is the short-lived read link a DTO carries. Presigned rather than
	// public, because an identity scan and a product photo live in the same bucket and only
	// one of them may be world-readable.
	PresignDownload(ctx context.Context, objectKey string, ttl time.Duration) (string, time.Time, error)
	// Fetch reads the bytes back. Needed where this process itself has to look at an object
	// rather than hand a link to somebody who will — a model reading a listing photo, say. A
	// store that only serves other people's origins answers ErrNotReadable.
	Fetch(ctx context.Context, objectKey string) (Blob, error)
	// Remove deletes the object. Called by the reaper after a row is soft-deleted, so the
	// bytes go once nothing can still be pointing at them.
	Remove(ctx context.Context, objectKey string) error
}
