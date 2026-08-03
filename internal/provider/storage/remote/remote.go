// Package remote is the store for objects this platform does not hold: an image already
// served from somebody else's origin, addressed by its own URL.
//
// It is a real provider rather than a special case in the resolver, because "where do these
// bytes live" is the question `resource.provider` exists to answer, and "not here" is one of
// the answers. That keeps every caller on one path: a listing photo imported from a CDN and
// one uploaded to a bucket are resolved by the same code, and neither knows the difference.
//
// Read-only by nature. The object key *is* the link, so there is nothing to sign, nothing to
// expire, and no credential to configure — which is also why it can be wired unconditionally
// while every writable store needs a selector and its secrets.
package remote

import (
	"context"
	"net/url"
	"time"

	"shopnexus/internal/provider/storage"
)

// Name is the value stored in `resource.provider`.
const Name = "remote"

type Client struct{}

func New() *Client { return &Client{} }

var _ storage.Client = (*Client)(nil)

func (c *Client) Name() string { return Name }

// PresignUpload refuses. Nothing routes here — an upload goes to Registry.Write — so reaching
// this is a wiring mistake, and answering it would mean handing out a slot at an origin this
// platform does not control.
func (c *Client) PresignUpload(ctx context.Context, req storage.NewUpload) (storage.Upload, error) {
	return storage.Upload{}, storage.ErrProviderReadOnly
}

// Stat refuses for the same reason: it is the confirm step's check that bytes arrived, and
// nothing uploads here. Reading the origin to answer it would make rendering a page depend on
// a third party being up.
func (c *Client) Stat(ctx context.Context, objectKey string) (storage.Object, error) {
	return storage.Object{}, storage.ErrProviderReadOnly
}

// PresignDownload hands the key straight back, since for this store the key is the address.
//
// The zero expiry is not a missing value: the link is the origin's own and this platform has
// no say in how long it lives, so claiming a deadline would be inventing one. Callers render
// no `url_expires_at` for it.
func (c *Client) PresignDownload(ctx context.Context, objectKey string, ttl time.Duration) (string, time.Time, error) {
	if !servableURL(objectKey) {
		return "", time.Time{}, storage.ErrObjectNotFound
	}
	return objectKey, time.Time{}, nil
}

// Remove succeeds without doing anything. These bytes belong to whoever serves them, so there
// is nothing here to delete — and an error would put the reaper in a loop over a row it can
// never finish, for an object it was never allowed to touch.
func (c *Client) Remove(ctx context.Context, objectKey string) error { return nil }

// servableURL is the check that keeps this store from becoming an open redirect in a DTO. The
// key goes into the page as a link, so only an absolute http(s) URL with a host is allowed:
// `javascript:` and `data:` are links a browser will happily run.
func servableURL(objectKey string) bool {
	u, err := url.Parse(objectKey)
	if err != nil {
		return false
	}
	return (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}
