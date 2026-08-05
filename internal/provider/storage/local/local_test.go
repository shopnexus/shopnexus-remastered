package local_test

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"shopnexus/internal/provider/storage"
	"shopnexus/internal/provider/storage/local"
)

func newClient(t *testing.T) *local.Client {
	t.Helper()
	c, err := local.New(local.Config{
		Root:         t.TempDir(),
		BaseURL:      "http://localhost:5000/api/v1",
		Secret:       "0123456789abcdef0123456789abcdef",
		UploadTTL:    15 * time.Minute,
		DownloadTTL:  time.Hour,
		MaxSize:      64,
		MaxVideoSize: 512,
		AllowedMimes: []string{"image/jpeg", "image/png", "video/mp4"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// slot pulls the query out of a signed URL, which is what the gateway route reads.
func slot(t *testing.T, raw string) url.Values {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse signed URL: %v", err)
	}
	return parsed.Query()
}

// The whole slot lifecycle: presign, PUT, confirm, read back.
func TestUpload_RoundTrip(t *testing.T) {
	c := newClient(t)
	up, err := c.PresignUpload(t.Context(), storage.NewUpload{
		Prefix: "catalog/listing", Filename: "front.JPG", Mime: "image/jpeg", Size: 12,
	})
	if err != nil {
		t.Fatalf("PresignUpload: %v", err)
	}
	// The key is generated and keeps only the extension: a client-chosen path is a traversal
	// waiting for a backend that resolves it.
	if !strings.HasPrefix(up.ObjectKey, "catalog/listing/") || !strings.HasSuffix(up.ObjectKey, ".jpg") {
		t.Fatalf("object key = %q, want a generated key under the prefix", up.ObjectKey)
	}
	if strings.Contains(up.ObjectKey, "front") {
		t.Errorf("object key %q kept the client's filename", up.ObjectKey)
	}

	q := slot(t, up.URL)
	if err := c.Verify("PUT", q.Get("key"), q.Get("expires"), q.Get("signature")); err != nil {
		t.Fatalf("Verify the slot it just issued: %v", err)
	}
	// Nothing is there until the client uploads, which is what the confirm step checks.
	if _, err := c.Stat(t.Context(), up.ObjectKey); !errors.Is(err, storage.ErrObjectNotFound) {
		t.Fatalf("Stat before the PUT = %v, want ErrObjectNotFound", err)
	}
	if _, err := c.Write(up.ObjectKey, strings.NewReader("hello bytes")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	object, err := c.Stat(t.Context(), up.ObjectKey)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if object.Size != 11 || object.Mime != "image/jpeg" {
		t.Fatalf("object = %+v, want the bytes that arrived", object)
	}

	link, expires, err := c.PresignDownload(t.Context(), up.ObjectKey, 0)
	if err != nil {
		t.Fatalf("PresignDownload: %v", err)
	}
	if !expires.After(time.Now()) {
		t.Errorf("download link expires at %v, which is not in the future", expires)
	}
	q = slot(t, link)
	if err := c.Verify("GET", q.Get("key"), q.Get("expires"), q.Get("signature")); err != nil {
		t.Fatalf("Verify the download link: %v", err)
	}
	body, mime, err := c.Open(up.ObjectKey)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer body.Close()
	if mime != "image/jpeg" {
		t.Errorf("mime = %q, want the stored type", mime)
	}
}

// A signature covers the method, the key and the expiry — each of them, or the slot is
// something other than what it was issued for.
func TestVerify_RefusesAnythingButTheSlotItSigned(t *testing.T) {
	c := newClient(t)
	up, err := c.PresignUpload(t.Context(), storage.NewUpload{
		Prefix: "trust/review", Filename: "a.png", Mime: "image/png", Size: 10,
	})
	if err != nil {
		t.Fatalf("PresignUpload: %v", err)
	}
	q := slot(t, up.URL)
	key, expires, sig := q.Get("key"), q.Get("expires"), q.Get("signature")

	cases := map[string]struct{ method, key, expires, sig string }{
		// A write slot must not double as a readable link, and a read link must not let
		// anybody overwrite the object.
		"read with a write signature":    {"GET", key, expires, sig},
		"another key, same signature":    {"PUT", "trust/review/somebody-elses", expires, sig},
		"a later expiry, same signature": {"PUT", key, "99999999999", sig},
		"a forged signature":             {"PUT", key, expires, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
		"no signature at all":            {"PUT", key, expires, ""},
		"an unparseable expiry":          {"PUT", key, "soon", sig},
	}
	for name, tc := range cases {
		if err := c.Verify(tc.method, tc.key, tc.expires, tc.sig); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}

	// And an expired slot is refused even with its own signature.
	stale, err := local.New(local.Config{
		Root: t.TempDir(), BaseURL: "http://localhost:5000/api/v1",
		Secret: "0123456789abcdef0123456789abcdef",
		// A one-nanosecond slot is how "already expired" is produced without sleeping.
		UploadTTL: time.Nanosecond, DownloadTTL: time.Hour, MaxSize: 64, MaxVideoSize: 512,
		AllowedMimes: []string{"image/png"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	old, err := stale.PresignUpload(t.Context(), storage.NewUpload{
		Prefix: "trust/review", Filename: "a.png", Mime: "image/png", Size: 10,
	})
	if err != nil {
		t.Fatalf("PresignUpload: %v", err)
	}
	q = slot(t, old.URL)
	if err := stale.Verify("PUT", q.Get("key"), q.Get("expires"), q.Get("signature")); err == nil {
		t.Error("an expired slot was accepted")
	}
}

// A key that climbs out of the root is refused. The check is on the resolved path, because
// "a/../../etc/passwd" only obviously escapes after it is resolved.
func TestPath_RefusesTraversal(t *testing.T) {
	c := newClient(t)
	for _, key := range []string{
		"../escaped", "catalog/../../escaped", "/etc/passwd", "",
	} {
		if _, err := c.Write(key, strings.NewReader("x")); err == nil {
			t.Errorf("Write(%q) escaped the root", key)
		}
		if _, err := c.Stat(t.Context(), key); err == nil {
			t.Errorf("Stat(%q) escaped the root", key)
		}
	}
}

// The two refusals that happen before a byte moves: a type the platform does not store, and a
// declared size over the limit.
func TestPresignUpload_RefusesWhatItWillNotStore(t *testing.T) {
	c := newClient(t)
	if _, err := c.PresignUpload(t.Context(), storage.NewUpload{
		Prefix: "catalog/listing", Filename: "x.html", Mime: "text/html", Size: 10,
	}); !errors.Is(err, storage.ErrMimeNotAllowed) {
		t.Fatalf("err = %v, want ErrMimeNotAllowed — a stored text/html is a stored XSS", err)
	}
	if _, err := c.PresignUpload(t.Context(), storage.NewUpload{
		Prefix: "catalog/listing", Filename: "x.jpg", Mime: "image/jpeg", Size: 1 << 20,
	}); !errors.Is(err, storage.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

// A client that declares a small size and then sends more is cut off at the limit rather than
// filling the disk.
func TestWrite_RefusesMoreThanTheLimit(t *testing.T) {
	c := newClient(t)
	up, err := c.PresignUpload(t.Context(), storage.NewUpload{
		Prefix: "order/receipt", Filename: "r.jpg", Mime: "image/jpeg", Size: 10,
	})
	if err != nil {
		t.Fatalf("PresignUpload: %v", err)
	}
	if _, err := c.Write(up.ObjectKey, strings.NewReader(strings.Repeat("x", 65))); !errors.Is(err, storage.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	// And nothing was left behind: the write goes to a temporary file and is renamed only
	// once it is known to be within the limit.
	if _, err := c.Stat(t.Context(), up.ObjectKey); !errors.Is(err, storage.ErrObjectNotFound) {
		t.Fatalf("Stat = %v, want the refused upload to leave no object", err)
	}
}

// A video is an order of magnitude bigger than a photo, so it has its own limit. One limit for both
// would have to be the video's, and a slot signed for a 512-byte "avatar" is then what a photo route
// hands out.
func TestPresignUpload_VideoHasItsOwnLimit(t *testing.T) {
	c := newClient(t)

	if _, err := c.PresignUpload(context.Background(), storage.NewUpload{
		Prefix: "order/receipt", Filename: "unboxing.mp4", Mime: "video/mp4", Size: 300,
	}); err != nil {
		t.Fatalf("PresignUpload(video under its limit): %v", err)
	}
	if _, err := c.PresignUpload(context.Background(), storage.NewUpload{
		Prefix: "order/receipt", Filename: "film.mp4", Mime: "video/mp4", Size: 513,
	}); !errors.Is(err, storage.ErrTooLarge) {
		t.Fatalf("PresignUpload(video over its limit) = %v, want ErrTooLarge", err)
	}
	// The image limit does not move with it.
	if _, err := c.PresignUpload(context.Background(), storage.NewUpload{
		Prefix: "listing", Filename: "big.png", Mime: "image/png", Size: 300,
	}); !errors.Is(err, storage.ErrTooLarge) {
		t.Fatalf("PresignUpload(image over its limit) = %v, want ErrTooLarge", err)
	}
	// A type nobody allowed is refused on the type, whatever its size: answering "too large" to a
	// stored script would tell the caller to try a smaller one.
	if _, err := c.PresignUpload(context.Background(), storage.NewUpload{
		Prefix: "listing", Filename: "x.html", Mime: "text/html", Size: 1,
	}); !errors.Is(err, storage.ErrMimeNotAllowed) {
		t.Fatalf("PresignUpload(html) = %v, want ErrMimeNotAllowed", err)
	}
}

// The write path holds the bytes to the same limit the presign promised, keyed off what the object
// actually is — otherwise a slot signed for a photo receives a film.
func TestWrite_HoldsEachTypeToItsOwnLimit(t *testing.T) {
	c := newClient(t)
	slot, err := c.PresignUpload(context.Background(), storage.NewUpload{
		Prefix: "order/receipt", Filename: "unboxing.mp4", Mime: "video/mp4", Size: 300,
	})
	if err != nil {
		t.Fatalf("PresignUpload: %v", err)
	}
	if _, err := c.Write(slot.ObjectKey, bytes.NewReader(make([]byte, 300))); err != nil {
		t.Fatalf("Write(300 bytes of video): %v", err)
	}
	if _, err := c.Write(slot.ObjectKey, bytes.NewReader(make([]byte, 513))); !errors.Is(err, storage.ErrTooLarge) {
		t.Fatalf("Write(over the video limit) = %v, want ErrTooLarge", err)
	}
}
